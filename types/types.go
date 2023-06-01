package types

import (
	"io"

	"github.com/pion/webrtc/v3"
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

type P2POperations interface {
	SendICECandidate(id string, candidate *webrtc.ICECandidate) error
	OnICECandidate(id string, candidate *webrtc.ICECandidate) error
	SendOffer(id string, offer *webrtc.SessionDescription) error
	OnAnswer(id string, answer *webrtc.SessionDescription) error
}
