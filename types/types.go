package types

import (
	"io"

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

type Marshaler interface {
	Marshal(v interface{}) ([]byte, error)
}

type Unmarshaler interface {
	Unmarshal([]byte, interface{}) error
}

type Encoder interface {
	Encode(interface{}) error
}

type Decoder interface {
	Decode(interface{}) error
}

type CreateEncoder interface {
	CreateEncoder(io.Writer) Encoder
}

type CreateDecoder interface {
	CreateDecoder(io.Reader) Decoder
}

type Serializer interface {
	CreateEncoder
	Marshaler
}

type Deserializer interface {
	CreateDecoder
	Unmarshaler
}

type SerDe interface {
	Serializer
	Deserializer
}

type Chunk struct {
	ID   uint64
	Seq  uint64
	End  bool
	Data []byte
}
