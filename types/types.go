package types

import (
	"github.com/pion/webrtc/v3"
)

type Packet struct {
	ConnectionID string      `json:"connectionID"`
	Type         PacketType  `json:"type"`
	Data         interface{} `json:"data"`
}

type SignalPacket struct {
	*Packet
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

type PacketType string

const (
	CandidatePacketType PacketType = "candidate"
	OfferPacketType     PacketType = "offer"
	SDPPacketType       PacketType = "sdp"
)

type P2POperations interface {
	SendICECandidate(id string, candidate *webrtc.ICECandidate) error
	OnICECandidate(id string, candidate *webrtc.ICECandidate) error
	SendOffer(id string, offer *webrtc.SessionDescription) error
	OnAnswer(id string, answer *webrtc.SessionDescription) error
}

type Decoder interface {
	Decode() (any, error)
}

type Encoder interface {
	Encode(interface{}) error
}
