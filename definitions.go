// TCP Socket Handling Library
// (c) 2019 Liquid Telecommunications - Please see LICENSE for licensing rights

/*
Various structural definitions and the packet buffer size that we will attempt to read at each iteration
*/

package lsock
import (
    "net"
    "sync"
    "time"
)

const PACKET_BUFFER_SIZE = 1

// Socket actions
const (
	_ = iota
	NEW_SOCKET byte = iota +1
	CLOSE_SOCKET
	RECONNECT_SOCKET
	REGISTER_SOCKET
	RETRIEVE_SOCKETS
)

// Control types when passing data
const (
	_ = iota
	NEW_DATA byte = iota + 1
	SOCKET_CLOSED
)

// This is our structure for sockets, and contains various channels for control, the socket itself
// and several other useful pieces.  Additional session based data can be stored in in the Supplementary
// interface for passing to third party packet processing routines
type Lsock struct {
    Peer string
    Port uint16
    Timeout uint16
    Control chan *ControlMsg
    Output chan []byte
	ReadControl chan byte
	WriteChannel chan *DataMsg
	TimeoutControl chan bool
    Socket net.Conn
    RetryCount uint16
    RetryTimer byte
    Mutex *sync.Mutex
    Stats *SockStats
    State bool
	IsInbound bool
	Supplementary interface{}
}

type SockStats struct {
    Read uint64
    Wrote uint64
    Disconnects uint16
    Reconnects uint16
	CurrentRetries uint16
    LastReconnect time.Time
    LastDisconnect time.Time
    ReconnectCount byte
    LastRead time.Time
    LastWrote time.Time
}

type ControlMsg struct {
    Sock *Lsock
    Action byte
}

type DataMsg struct {
	Data []byte
	ControlType byte
}

type TrackControl struct {
	Action byte
	SockStruct *Lsock
	RetrievalChannel chan []*Lsock
}

