// TCP Socket Handling Library
// (c) 2019 Liquid Telecommunications - Please see LICENSE for licensing rights

/*
This library provides generic tcp socket handling functionality - to use:

It is imperative that a new channel of type *TrackControl be initiatiated and a go routine of SockerTracker() be started with the
channel in question.

To dial a socket:
Call lsock.NewConnection("ipaddress", port, ReadTimeOutInSeconds, DataOutputChannel, RetryTimer, RetryCount, TrackerChannel)

RetryTimer is the number of seconds between re-dial attempts should a reconnection be required
RetryCount is the number of times it will attempt to reconnect before failing entirely, if 0, retry forever
Tracker Channel is the previously referred to TrackControl channel used to register sockets with the socket tracker

To listen on a socket call NewListener() and ensure you pass it a tracker control channel for socket registration
*/

package lsock
import (
	"net"
	"sync"
	"time"
	"fmt"
	"bytes"
	"io"
	"strings"
	"strconv"
)

// Forms a new connection and returns an LSock structure complete with attached statistics structure
func NewConnection(Peer string, Port uint16, Timeout uint16, Output chan []byte, RetryTimer byte, RetryCount uint16, Tracker chan *TrackControl) *Lsock{
	var result *Lsock

	result = &Lsock{
		Peer: Peer,
		Port: Port,
		Timeout: Timeout,
		TimeoutControl: make(chan bool),
		RetryTimer: RetryTimer,
		RetryCount: RetryCount,
		Output: Output,
		ReadControl: make(chan byte),
		WriteChannel: make(chan *DataMsg),
		Stats: &SockStats{ReconnectCount: 0},
		Control: make(chan *ControlMsg),
		Mutex: &sync.Mutex{},
		IsInbound: false,
	}

	go ConnectionHandler(result.Control, Tracker)
	result.Control <- &ControlMsg{result, NEW_SOCKET}
	return result
}

// Listens for new connections and fires our reader and timeout watchdogs correctly
func NewListener(LocalAddress, Port uint16, Timeout uint16, Output chan []byte, RegisterChannel chan *TrackControl) {
	var RetryCount int = 0
	var listener net.Listener
	var err error

	for {
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", LocalAddress, Port))
		if err != nil {
			RetryCount++
			if RetryCount > 10 {
				DebugLog(fmt.Sprintf("Excessive listen failures, giving up listening on %s:%d",LocalAddress,Port))
				return
			} else {
				continue
			}
		}
		break
	}
	for {
		socket,err := listener.Accept()
		if err != nil {
			DebugLog("Failed to Accept(), moving on....")
		}
		RemoteAddr := strings.Split(socket.RemoteAddr().String(), ":")
		Port,_ := strconv.Atoi(RemoteAddr[1])
		DebugLog(fmt.Sprintf("Got new connection from %s",socket.RemoteAddr().String()))
		NewSocket := &Lsock{
			Peer: RemoteAddr[0],
			Port: uint16(Port),
			Timeout: Timeout,
			TimeoutControl: make(chan bool),
			Mutex: &sync.Mutex{},
			Control: make(chan *ControlMsg),
			Stats: &SockStats{},
			Output: Output,
			ReadControl: make(chan byte),
			WriteChannel: make(chan *DataMsg),
			Socket: socket,
		}
		go NewSocket.Reader()
		go NewSocket.Writer()
		go NewSocket.TimeoutWatchdog()
		go ConnectionHandler(NewSocket.Control, RegisterChannel)
		RegisterChannel <- &TrackControl{REGISTER_SOCKET, NewSocket, nil}
	}
}

func SocketTracker(Action chan *TrackControl) {
	var ActiveSockets map[string]*Lsock = make(map[string]*Lsock)

	for {
		select {
			case Input :=<-Action:
				switch Input.Action {
					case REGISTER_SOCKET:
						ActiveSockets[fmt.Sprintf("%s:%d",Input.SockStruct.Peer,Input.SockStruct.Port)] = Input.SockStruct
					case CLOSE_SOCKET:
						delete(ActiveSockets, fmt.Sprintf("%s:%d", Input.SockStruct.Peer,Input.SockStruct.Port))
						Input.SockStruct.Mutex.Unlock()
					case RETRIEVE_SOCKETS:
						var SocketPointerArray []*Lsock =  make([]*Lsock, 0)
						for _,sock := range ActiveSockets {
							SocketPointerArray = append(SocketPointerArray, sock)
						}
						Input.RetrievalChannel <- SocketPointerArray
				}
		}
	}
}

// Our socket handler - used for opening closing and reconnecting sockets
func ConnectionHandler(in chan *ControlMsg, Tracker chan *TrackControl) {
	var err error
	for {
		select {
			case input :=<-in:
				switch input.Action {
					case NEW_SOCKET:
						DebugLog(fmt.Sprintf("Got new connection request for %s:%d...",input.sock.Peer, input.sock.Port))
						if input.sock.Socket, err = net.Dial("tcp", fmt.Sprintf("%s:%d", input.sock.Peer,  input.sock.Port)); err != nil {
							// if RetryCount is 0 - keep retrying endlessly
							if input.sock.Stats.CurrentRetries >= input.sock.RetryCount && input.sock.RetryCount != 0 {
								DebugLog("Connection retry socket count exceeded")
								return
							} else {
								DebugLog("Failed to connect to %s:%d, scheduling retry...\n")
								input.sock.Stats.CurrentRetries++
								go ReconnectSocket(input.sock)
							}
						} else {
							input.sock.Stats.CurrentRetries = 0
							input.sock.Stats.Reconnects++
							input.sock.Stats.LastReconnect = time.Now()
							input.sock.Stats.LastRead = time.Now()
							DebugLog(fmt.Sprintf("Got socket connection to %s:%d, starting reader and time out functions", input.sock.Peer, input.sock.Port))
							go input.sock.Reader()
							go input.sock.Writer()
							go input.sock.TimeoutWatchdog()
							Tracker <- &TrackControl{REGISTER_SOCKET, input.sock, nil}
							DebugLog(fmt.Sprintf("Spawned relevant routines for %s:%d...", input.sock.Peer, input.sock.Port))
						}
					case RECONNECT_SOCKET:
						DebugLog(fmt.Sprintf("Got socket reconnection request for %s:%d, resetting TCP session", input.sock.Peer, input.sock.Port))
						DebugLog(fmt.Sprintf("Stopping read routine for %s:%d...",input.sock.Peer, input.sock.Port))
						input.sock.ReadControl <- SOCKET_CLOSED
						DebugLog(fmt.Sprintf("Stopping write routine for %s:%d...",input.sock.Peer, input.sock.Port))
						input.sock.WriteChannel <- &DataMsg{ControlType: SOCKET_CLOSED}
						DebugLog(fmt.Sprintf("Stopping timeout watchdog routine for %s:%d...",input.sock.Peer, input.sock.Port))
						input.sock.TimeoutControl <- true
						DebugLog(fmt.Sprintf("Closing socket for %s:%d...",input.sock.Peer, input.sock.Port))
						input.sock.Mutex.Lock()
						Tracker <- &TrackControl{CLOSE_SOCKET, input.sock, nil}
						input.sock.Socket.Close()
						go ReconnectSocket(input.sock)
					case CLOSE_SOCKET:
						input.sock.ReadControl <- SOCKET_CLOSED
						input.sock.WriteChannel <- &DataMsg{ControlType: SOCKET_CLOSED}
						input.sock.TimeoutControl <- true
						input.sock.Mutex.Lock()
						Tracker <- &TrackControl{CLOSE_SOCKET, input.sock, nil}
						input.sock.Socket.Close()
				}
		}
	}
}

// ReconnectSocket waits for the reconnection time and then instructs the connection handler to reconnect the socket
func ReconnectSocket(s *Lsock) {
	DebugLog(fmt.Sprintf("Got reconnection request to %s:%d, sleeping %d seconds before reconnect",s.Peer,s.Port, s.RetryTimer))
	time.Sleep(time.Duration(s.RetryTimer)*time.Second)
	DebugLog(fmt.Sprintf("Sending connection control request for %s:%d...\n",s.Peer,s.Port))
	s.Control <- &ControlMsg{s, NEW_SOCKET}
	return
}

// Our socket reader function
func (s *Lsock) Reader() {
	var InputBuffer bytes.Buffer
	var ByteBuffer *[]byte

	for {
		select {
			case ControlAction :=<-s.ReadControl:
				if ControlAction == SOCKET_CLOSED {
					DebugLog(fmt.Sprintf("Got request to close socket for %s:%d... closing reader routine...", s.Peer, s.Port))
					return
				} else {
					DebugLog(fmt.Sprintf("Got some other action [%d] on read control for %s:%d\n",s.Peer,s.Port))
				}
			default:
				s.Socket.SetDeadline(time.Now().Add(200 * time.Millisecond))
				_,err := io.CopyN(&InputBuffer, s.Socket, PACKET_BUFFER_SIZE)
				if err, ok := err.(net.Error); ok && err.Timeout() {
					continue
				} else if err != nil {
					DebugLog(fmt.Sprintf("Error reading data from socket %s:%d: %v", s.Peer, s.Port, err))
					if !s.IsInbound {
						s.Control <- &ControlMsg{s, RECONNECT_SOCKET}
					} else {
						s.Control <- &ControlMsg{s, CLOSE_SOCKET}
					}
				}
				ByteBuffer = new([]byte)
				copy(*ByteBuffer, InputBuffer.Bytes())
				s.Stats.Read += PACKET_BUFFER_SIZE
				s.Stats.LastRead = time.Now()
				s.Output <- *ByteBuffer
				InputBuffer.Reset()
		}
	}
}

// Our socket writer
func (s *Lsock) Writer() {
	DebugLog(fmt.Sprintf("Socket writer for %s:%d spawned", s.Peer,s.Port))
	for {
		select {
			case WriteMsg := <-s.WriteChannel:
				DebugLog(fmt.Sprintf("Got an inbound message on the write channel for %s:%d...\n",s.Peer,s.Port))
				switch WriteMsg.ControlType {
					case SOCKET_CLOSED:
						DebugLog(fmt.Sprintf("Socket %s:%d closed by request, closing writer routine...\n", s.Peer, s.Port))
						return
					case NEW_DATA:
						s.Socket.Write(WriteMsg.Data)
						s.Stats.Wrote += uint64(len(WriteMsg.Data))
						s.Stats.LastWrote = time.Now()
				}
		}
	}
}

// Our socket timeout function, if the Timeout is reached between reads, tear down the socket and schedule it 
// for reconnection
func (s *Lsock) TimeoutWatchdog() {
	DebugLog(fmt.Sprintf("Timeout watchdog for %s:%d spawned", s.Peer, s.Port))
	for {
		select {
			case _ =<-s.TimeoutControl:
				DebugLog(fmt.Sprintf("Got request to close socket %s:%d... Closing timeout controller...\n", s.Peer, s.Port))
				return
			default:
				if uint16(time.Since(s.Stats.LastRead).Seconds()) > s.Timeout {
					s.Stats.LastRead = time.Now()
					DebugLog(fmt.Sprintf("Timeout elapsed reading from %s:%d, closing socket and scheduling reconnect", s.Peer, s.Port))
					s.Control <- &ControlMsg{s, RECONNECT_SOCKET}
					DebugLog(fmt.Sprintf("Sent reconnection request for %s:%d...", s.Peer, s.Port))
				} else {
					time.Sleep(time.Duration(200) * time.Millisecond)
				}
		}
	}
}
