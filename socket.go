// TCP Socket Handling Library
// (c) 2019 Liquid Telecommunications - Please see LICENSE for licensing rights

/*
This library provides generic tcp socket handling functionality - to use:
Call lsock.NewConnection("ipaddress", port, ReadTimeOutInSeconds, DataOutputChannel, RetryTimer, RetryCount)

RetryTimer is the number of seconds between re-dial attempts should a reconnection be required
RetryCount is the number of times it will attempt to reconnect before failing entirely, if 0, retry forever
*/

package lsock
import (
	"net"
	"sync"
	"time"
	"fmt"
	"bytes"
	"io"
)

// Forms a new connection and returns an LSock structure complete with attached statistics structure
func NewConnection(Peer string, Port uint16, Timeout uint16, Output chan []byte, RetryTimer byte, RetryCount uint16) *Lsock{
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
	}

	go ConnectionHandler(result.Control)
	result.Control <- &ControlMsg{result, NEW_SOCKET}
	return result
}

// Our socket handler - used for opening closing and reconnecting sockets
func ConnectionHandler(in chan *ControlMsg) {
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
						input.sock.Socket.Close()
						go ReconnectSocket(input.sock)
					case CLOSE_SOCKET:
						input.sock.ReadControl <- SOCKET_CLOSED
						input.sock.WriteChannel <- &DataMsg{ControlType: SOCKET_CLOSED}
						input.sock.Socket.Close()
						input.sock.TimeoutControl <- true
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
					s.Control <- &ControlMsg{s, RECONNECT_SOCKET}
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
