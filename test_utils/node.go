package test_utils

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/gurupras/maxp2p/types"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

type PeerConnection struct {
	UUID string
	*webrtc.PeerConnection
}

type Node struct {
	ID string
	sync.Mutex
	api         *webrtc.API
	config      *webrtc.Configuration
	connections map[string]*PeerConnection
	*ServerConnection
	pendingCandidates map[string][]*webrtc.ICECandidate
	StopServerConn    uint32
	WaitGroup         sync.WaitGroup
	onError           func(error)
	onPeerConnection  func(*webrtc.PeerConnection)
	onOffer           func(src string, connID string, offer *webrtc.SessionDescription)
	onSDP             func(src string, connID string, sdp *webrtc.SessionDescription)
	onICECandidate    func(src string, connID string, c *webrtc.ICECandidateInit)
}

func NewNode(id string, urlStr string, api *webrtc.API, config *webrtc.Configuration) (*Node, error) {
	conn, err := NewServerConnection(id, urlStr)
	if err != nil {
		return nil, err
	}
	ret := &Node{
		ID:                id,
		Mutex:             sync.Mutex{},
		api:               api,
		config:            config,
		connections:       make(map[string]*PeerConnection),
		ServerConnection:  conn,
		pendingCandidates: make(map[string][]*webrtc.ICECandidate),
		StopServerConn:    0,
		onError:           nil,
		WaitGroup:         sync.WaitGroup{},
	}
	return ret, nil
}

func (n *Node) Close() error {
	var err error
	if atomic.LoadUint32(&n.StopServerConn) == 1 {
		// Already stopped
		return nil
	}
	atomic.StoreUint32(&n.StopServerConn, 1)
	// Cleanly close the connection by sending a close message and then
	// waiting (with timeout) for the server to close the connection.
	if err := n.ServerConnection.Close(); err != nil {
		log.Warnf("[%v]: Failed to cleanly terminate connection with server: %v\n", n.ID, err)
	}
	n.WaitGroup.Wait()
	for _, pc := range n.connections {
		err = pc.Close()
	}
	return err
}

func (n *Node) OnPeerConnection(cb func(*webrtc.PeerConnection)) {
	n.Lock()
	defer n.Unlock()
	n.onPeerConnection = cb
}

func (n *Node) Offer(peer string, cb func(*webrtc.PeerConnection)) error {
	pc, err := n.api.NewPeerConnection(*n.config)
	if err != nil {
		return nil
	}
	if cb != nil {
		cb(pc)
	}
	connID := gonanoid.Must()
	func() {
		n.Lock()
		defer n.Unlock()
		n.connections[connID] = &PeerConnection{
			UUID:           connID,
			PeerConnection: pc,
		}
	}()

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		n.Lock()
		defer n.Unlock()
		pc, ok := n.connections[connID]
		if !ok {
			log.Warnf("[%v]: ICE candidate for unknown connection with id '%v' from peer '%v'", n.ID, connID, peer)
			return
		}
		desc := pc.RemoteDescription()
		if desc == nil {
			pending, ok := n.pendingCandidates[connID]
			if !ok {
				pending = make([]*webrtc.ICECandidate, 0)
				n.pendingCandidates[connID] = pending
			}
			n.pendingCandidates[connID] = append(pending, c)
			log.Debugf("[%v]: Got (pending) ICE candidate for connection with id '%v' from peer '%v': %v\n", n.ID, connID, peer, c)
		} else {
			if err := n.SendCandidate(peer, connID, c); err != nil {
				fmt.Printf("[%v]: Could not send candidate info for connection with id '%v' to peer '%v': %v", n.ID, connID, peer, err)
				return
			}
			log.Debugf("[%v]: Sent ICE candidate for connection with id '%v' from peer '%v': %v\n", n.ID, connID, peer, c)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Errorf("[%v]: Failed to create offer for connection with id '%v' for peer '%v': %v\n", n.ID, connID, peer, err)
		return err
	}

	offerGatheringComplete := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		log.Errorf("[%v]: Failed to set local description for connection with id '%v' for peer '%v': %v\n", n.ID, connID, peer, err)
		return err
	}
	<-offerGatheringComplete
	if err := n.SendOffer(peer, connID, &offer); err != nil {
		log.Errorf("[%v]: Failed to send offer for connection with id '%v' for peer '%v': %v\n", n.ID, connID, peer, err)
		return err
	}
	return nil
}

func ParseSDP(sdpBytes []byte) (*webrtc.SessionDescription, error) {
	sdp := webrtc.SessionDescription{}
	json.Unmarshal(sdpBytes, &sdp)
	if err := json.Unmarshal(sdpBytes, &sdp); err != nil {
		log.Errorf("Failed to parse SDP: %v\n", err)
		return nil, err
	}
	return &sdp, nil
}

func (n *Node) HandleServerMessages() {
	n.WaitGroup.Add(1)
	defer n.WaitGroup.Done()

	for atomic.LoadUint32(&n.StopServerConn) == 0 {
		_, message, err := n.ReadMessage()
		if err != nil {
			if _, ok := err.(*websocket.CloseError); !ok || atomic.LoadUint32(&n.StopServerConn) != 1 {
				// We haven't been asked to stop and we ran into an error. Log this.
				log.Errorf("[%v]: Encountered error when reading message from websocket: %v", n.ID, err)
			}
			break
		}
		var m map[string]interface{}
		err = json.Unmarshal(message, &m)
		if err != nil {
			log.Errorf("Failed to unmarshal json: %v\n", err)
			break
		}
		log.Debugf("[%v]: Received packet: %v\n", n.ID, m)

		packetType := types.PacketType(m["type"].(string))
		src := m["src"].(string)
		connID := m["connectionID"].(string)
		switch packetType {
		case types.OfferPacketType:
			{
				sdpBytes := []byte(m["data"].(string))
				sdp, err := ParseSDP(sdpBytes)
				if err != nil {
					log.Errorf("[%v]: Failed to parse SDP in offer packet from peer '%v': %v", n.ID, src, err)
					break
				}
				if n.onOffer != nil {
					n.onOffer(src, connID, sdp)
				} else {
					pc, err := n.api.NewPeerConnection(*n.config)
					if err != nil {
						log.Errorf("[%v]: Failed to set up new peer-connection: %v", n.ID, err)
						break
					}
					var connCallback func(*webrtc.PeerConnection)
					func() {
						n.Lock()
						defer n.Unlock()
						connCallback = n.onPeerConnection
						n.connections[connID] = &PeerConnection{
							UUID:           connID,
							PeerConnection: pc,
						}
					}()

					answerSent := uint32(0)
					pending := make([]*webrtc.ICECandidate, 0)

					pc.OnICECandidate(func(c *webrtc.ICECandidate) {
						if c == nil {
							return
						}
						if atomic.LoadUint32(&answerSent) == 1 {
							if err := n.SendCandidate(src, connID, c); err != nil {

							}
						} else {
							pending = append(pending, c)
						}
					})
					pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
						log.Debugf("[%v]: Connection state for connection with id '%v' to peer '%v' has changed: %s\n", n.ID, connID, src, s.String())

						if s == webrtc.PeerConnectionStateFailed {
							// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
							// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
							// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
							log.Errorf("[%v]: Peer Connection has gone to failed.. stopping transfer\n", n.ID)
						}
					})

					if connCallback != nil {
						connCallback(pc)
					}
					err = pc.SetRemoteDescription(*sdp)
					if err != nil {
						log.Errorf("[%v]: Failed to set remote description with offer from peer '%v': %v", n.ID, src, err)
						break
					}
					answer, err := pc.CreateAnswer(nil)
					if err != nil {
						log.Errorf("[%v]: Failed to create answer for offer from peer '%v': %v", n.ID, src, err)
						break
					}
					go func() {
						answerGatheringComplete := webrtc.GatheringCompletePromise(pc)
						if err := pc.SetLocalDescription(answer); err != nil {
							log.Errorf("[%v]: Failed to set answer as local description for connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
							return
						}
						<-answerGatheringComplete
						log.Debugf("[%v]: Gathering complete for connection with id '%v' from peer '%v'", n.ID, connID, src)

						// TODO: Send answer
						if err := n.SendSDP(src, connID, &answer); err != nil {
							log.Errorf("[%v]: Failed to send answer for connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
							return
						}
						atomic.StoreUint32(&answerSent, 1)
						// log.Debugf("[%v]: Sent answer SDP for connection with id '%v' from peer '%v': %v", n.id, connID, src, answer)

						for _, c := range pending {
							if err := n.SendCandidate(src, connID, c); err != nil {
								log.Errorf("[%v]: Failed to send ICE candidate for connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
								return
							}
						}
					}()
				}
			}
		case types.CandidatePacketType:
			{
				c := webrtc.ICECandidateInit{
					Candidate: m["data"].(string),
				}
				if n.onICECandidate != nil {
					n.onICECandidate(src, connID, &c)
				} else {
					pc, ok := n.connections[connID]
					if !ok {
						log.Errorf("[%v]: Unknown connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
						break
					}
					err := pc.AddICECandidate(c)
					if err != nil {
						log.Errorf("[%v]: Failed to add ICE candidate for connection with id '%v' from peer '%v': %v\n", n.ID, connID, src, err)
						break
					}
					log.Debugf("[%v]: Added ICE candidate for connection with id '%v' from peer: %v", n.ID, connID, src)
				}
			}
		case types.SDPPacketType:
			{
				sdpBytes := []byte(m["data"].(string))
				sdp, err := ParseSDP(sdpBytes)
				if err != nil {
					log.Errorf("[%v]: Failed to parse SDP in SDP packet for connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
					break
				}
				if n.onSDP != nil {
					n.onSDP(src, connID, sdp)
				} else {
					pc, ok := n.connections[connID]
					if !ok {
						log.Errorf("[%v]: Unknown connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
						break
					}
					if err = pc.SetRemoteDescription(*sdp); err != nil {
						log.Errorf("[%v]: Failed to set remote description with SDP for connection with id '%v' from peer '%v': %v", n.ID, connID, src, err)
						break
					}
				}
			}
		}
	}

}

func (n *Node) OnOffer(cb func(src string, connID string, offer *webrtc.SessionDescription)) {
	n.Lock()
	defer n.Unlock()
	n.onOffer = cb
}

func (n *Node) OnSDP(cb func(src string, connID string, offer *webrtc.SessionDescription)) {
	n.Lock()
	defer n.Unlock()
	n.onSDP = cb
}

func (n *Node) OnICECandidate(cb func(src string, connID string, c *webrtc.ICECandidateInit)) {
	n.Lock()
	defer n.Unlock()
	n.onICECandidate = cb
}
