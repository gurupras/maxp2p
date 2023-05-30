package test_utils

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gurupras/maxp2p/v2/types"
	"github.com/gurupras/maxp2p/v2/utils"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

type ServerConnection struct {
	*websocket.Conn
	deviceID string
	sync.Mutex
}

func NewServerConnection(deviceID, urlStr string) (*ServerConnection, error) {
	urlStr = utils.CreateDeviceIDQuery(deviceID, urlStr)
	// TODO: Send across accessToken for authentication
	log.Debugf("Server connection URL: %v\n", urlStr)

	c, _, err := websocket.DefaultDialer.Dial(urlStr, nil)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	errStr := "failed to receive initial ready signal. Does the backend send a ready signal?"
	if err = c.ReadJSON(&m); err != nil {
		log.Errorf("Error: %v: %v\n", errStr, err)
		return nil, err
	}
	if _, ok := m["action"]; !ok {
		err = fmt.Errorf(errStr)
		log.Errorf("Error: %v\n", err.Error())
		return nil, err
	}
	switch action := m["action"].(type) {
	case string:
		if action != "ready" {
			err = fmt.Errorf(errStr)
			log.Errorf("Error: %v\n", err.Error())
			return nil, err
		}
	default:
		err = fmt.Errorf(errStr)
		log.Errorf("Error: %v\n", err.Error())
		return nil, err
	}

	return &ServerConnection{c, deviceID, sync.Mutex{}}, nil
}

func (s *ServerConnection) SendCandidate(peer string, connID string, c *webrtc.ICECandidate) error {
	s.Lock()
	defer s.Unlock()
	b, _ := json.Marshal(c.ToJSON())
	pkt := &types.SignalPacket{
		Packet: &types.Packet{
			ConnectionID: connID,
			Type:         types.CandidatePacketType,
			Data:         string(b),
		},
		Src:  s.deviceID,
		Dest: peer,
	}
	err := s.WriteJSON(pkt)
	if err == nil {
		log.Debugf("[%v]: Sent ICE candidate for connection with id '%v' to peer '%v'", s.deviceID, connID, peer)
	}
	return err
}

func (s *ServerConnection) SendOffer(peer string, connID string, sdp *webrtc.SessionDescription) error {
	return s.SendSDP(peer, connID, sdp)
}

func (s *ServerConnection) SendSDP(peer string, connID string, sdp *webrtc.SessionDescription) error {
	s.Lock()
	defer s.Unlock()
	b, _ := json.Marshal(sdp)
	pkt := &types.SignalPacket{
		Packet: &types.Packet{
			ConnectionID: connID,
			Type:         types.SDPPacketType,
			Data:         string(b),
		},
		Src:  s.deviceID,
		Dest: peer,
	}
	err := s.WriteJSON(pkt)
	if err == nil {
		log.Debugf("[%v]: Sent SDP for connection with id '%v' to peer '%v' (type=%v): %v", s.deviceID, connID, peer, sdp.Type, string(b))
	}
	return err
}

func (s *ServerConnection) Close() error {
	return s.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Time{})
}
