package test_utils

import "github.com/pion/webrtc/v3"

type MaxP2PTestNode struct {
	*Node
}

func (n *MaxP2PTestNode) SendICECandidate(dest string, connID string, candidate *webrtc.ICECandidate) error {
	return n.Node.SendCandidate(dest, connID, candidate)
}

func (n *MaxP2PTestNode) SendOffer(dest string, connID string, offer *webrtc.SessionDescription) error {
	return n.Node.SendOffer(dest, connID, offer)
}

func (n *MaxP2PTestNode) SendSDP(dest string, connID string, sdp *webrtc.SessionDescription) error {
	return n.Node.SendSDP(dest, connID, sdp)
}
