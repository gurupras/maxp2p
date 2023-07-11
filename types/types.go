package types

import (
	"io"
)

type Named interface {
	Name() string
}

type NamedReadWriter interface {
	Named
	io.ReadWriter
}

type NamedWriteCloser interface {
	Named
	io.Writer
	io.Closer
}

type NamedReadWriteCloser interface {
	NamedReadWriter
	io.Closer
}

type Packet struct {
	ConnectionID string     `json:"connectionID"`
	Type         PacketType `json:"type"`
	Data         string     `json:"data"`
}

type SignalPacket struct {
	*Packet
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

type PacketType string

const (
	CandidatePacketType PacketType = "candidate"
	SDPPacketType       PacketType = "sdp"
)
