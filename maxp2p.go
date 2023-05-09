package maxp2p

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/gurupras/go-network"
	"github.com/gurupras/maxp2p/v2/utils"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

const MaxPacketSize = 65535

type Role string

const (
	Offerer  Role = "offerer"
	Answerer Role = "answerer"
)

type SignalFunc func(b []byte) error

type SendInterface interface {
	SendOffer(dest string, connID string, offer *webrtc.SessionDescription) error
	SendICECandidate(dest string, connID string, candidate *webrtc.ICECandidate) error
	SendSDP(dest string, connID string, sdp *webrtc.SessionDescription) error
}

type IncomingSignalInterface interface {
	OnOffer(src string, connID string, offer *webrtc.SessionDescription) error
	OnAnswer(src string, connID string, answer *webrtc.SessionDescription) error
	OnICECandidate(src string, connID string, candidate *webrtc.ICECandidate) error
}

type MaxP2P struct {
	sync.Mutex
	name                     string
	peer                     string
	iface                    SendInterface
	serde                    network.SerDe
	packetPool               *sync.Pool
	connections              map[string]*P2PConn
	unestablishedConnections map[string]*webrtc.PeerConnection
	pendingCandidates        map[string][]*webrtc.ICECandidate
	api                      *webrtc.API
	config                   webrtc.Configuration
	maxBufferSize            uint64
	writeChan                chan network.WritePacket
	serialWriteChan          chan network.WritePacket
	handleSerialWrites       sync.Once
	chunkSplitter            *network.ChunkSplitter
	chunkCombiner            *network.ChunkCombiner
	incomingDataChan         chan io.Reader
	connID                   uint32
	stopped                  bool
	onData                   func(data interface{}, discard func())
	onPeerConnection         func(*webrtc.PeerConnection)
}

type P2PConn struct {
	pc *webrtc.PeerConnection
	sync.Mutex
	combinedDC *utils.CombinedDC
	onData     func(data interface{}, discard func())
}

func (p *P2PConn) Close() error {
	p.combinedDC.Close()
	return p.pc.Close()
}

func New(name string, peer string, iface SendInterface, serde network.SerDe, createPacket func() interface{}, config webrtc.Configuration, maxBufferSize uint64) (*MaxP2P, error) {
	settings := webrtc.SettingEngine{}
	settings.DetachDataChannels()
	api := webrtc.NewAPI(webrtc.WithSettingEngine(settings))

	writeChan := make(chan network.WritePacket, 10)
	serialWriteChan := make(chan network.WritePacket, 10)
	incomingDataChan := make(chan io.Reader, 10)

	packetPool := &sync.Pool{
		New: func() any {
			return createPacket()
		},
	}

	ret := &MaxP2P{
		Mutex:                    sync.Mutex{},
		name:                     name,
		peer:                     peer,
		iface:                    iface,
		serde:                    serde,
		packetPool:               packetPool,
		api:                      api,
		config:                   config,
		connections:              make(map[string]*P2PConn),
		unestablishedConnections: make(map[string]*webrtc.PeerConnection),
		pendingCandidates:        make(map[string][]*webrtc.ICECandidate),
		maxBufferSize:            maxBufferSize,
		writeChan:                writeChan,
		serialWriteChan:          serialWriteChan,
		handleSerialWrites:       sync.Once{},
		chunkSplitter:            network.NewChunkSplitter(name, MaxPacketSize-64, serde, writeChan),
		chunkCombiner:            network.NewChunkCombiner(name, serde, incomingDataChan),
		incomingDataChan:         incomingDataChan,
		connID:                   0,
		stopped:                  false,
	}

	go ret.handleIncomingData()
	return ret, nil
}

func (m *MaxP2P) handleIncomingData() {
	for reader := range m.incomingDataChan {
		data, err := io.ReadAll(reader)
		if err != nil {
			log.Errorf("[%v]: Failed to read incoming packet: %v", m.name, err)
			return
		}
		pkt := m.packetPool.Get()
		err = m.serde.Unmarshal(data, &pkt)
		if err != nil {
			log.Errorf("[%v]: Failed to unmarshal incoming packet: %v", m.name, err)
			return
		} else {
			m.Lock()
			onData := m.onData
			m.Unlock()
			if onData != nil {
				onData(pkt, func() {
					m.packetPool.Put(pkt)
				})
			}

		}
	}
}

func (m *MaxP2P) Start(connections ...int) error {
	// We need at least one connection to be able to send/receive data
	// FIXME: Until bandwidth-based scaling is implemented, default to creating 30 connections and multiplexing these
	numConnections := 1
	if len(connections) != 0 {
		numConnections = connections[0]
	}

	var pcCallback func(*webrtc.PeerConnection)
	func() {
		m.Lock()
		defer m.Unlock()
		pcCallback = m.onPeerConnection
	}()

	for idx := 0; idx < numConnections; idx++ {
		conn, err := m.newPeerConnection(m.peer)
		if err != nil {
			return err
		}
		if pcCallback != nil {
			pcCallback(conn.pc)
		}
	}
	return nil
}

func (m *MaxP2P) Close() error {
	m.Lock()
	defer m.Unlock()
	m.stopped = true
	close(m.writeChan)
	close(m.serialWriteChan)
	m.chunkCombiner.Close()
	for _, conn := range m.connections {
		conn.Close()
	}
	return nil
}

func (m *MaxP2P) OnData(cb func(data interface{}, discard func())) {
	m.Lock()
	defer m.Unlock()
	m.onData = cb
	for _, conn := range m.connections {
		conn.Lock()
		conn.onData = cb
		conn.Unlock()
	}
}

func (m *MaxP2P) OnOffer(src string, connID string, offer *webrtc.SessionDescription) error {
	pc, err := m.api.NewPeerConnection(m.config)
	if err != nil {
		return err
	}

	var pcCallback func(*webrtc.PeerConnection)

	func() {
		m.Lock()
		defer m.Unlock()
		m.unestablishedConnections[connID] = pc
		pcCallback = m.onPeerConnection
	}()

	if pcCallback != nil {
		pcCallback(pc)
	}

	err = pc.SetRemoteDescription(*offer)
	if err != nil {
		return err
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}

	answerSent := uint32(0)
	pending := make([]*webrtc.ICECandidate, 0)

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		if atomic.LoadUint32(&answerSent) == 1 {
			if err := m.iface.SendICECandidate(src, connID, c); err != nil {

			}
		} else {
			pending = append(pending, c)
		}
	})

	moveConnected := sync.Once{}

	var combinedDC *utils.CombinedDC
	dcWG := sync.WaitGroup{}
	dcWG.Add(1)

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			defer dcWG.Done()
			combinedDC, err = utils.NewCombinedDC(dc, m.maxBufferSize)
			if err != nil {
				log.Errorf("[%v]: Failed to create combinedDC for connection with id '%v' from peer '%v': %v", m.name, connID, src, err)
				return
			}
		})
	})

	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		log.Debugf("[%v]: Connection state for connection with id '%v' to peer '%v' has changed: %s\n", m.name, connID, src, s.String())

		if s == webrtc.PeerConnectionStateFailed {
			// Wait until PeerConnection has had no network activity for 30 seconds or another failure. It may be reconnected using an ICE Restart.
			// Use webrtc.PeerConnectionStateDisconnected if you are interested in detecting faster timeout.
			// Note that the PeerConnection may come back from PeerConnectionStateDisconnected.
			log.Errorf("[%v]: Peer Connection has gone to failed.. stopping transfer\n", m.name)
		} else if s == webrtc.PeerConnectionStateConnected {
			moveConnected.Do(func() {
				dcWG.Wait()
				m.Lock()
				defer m.Unlock()
				conn := &P2PConn{
					pc:         pc,
					Mutex:      sync.Mutex{},
					combinedDC: combinedDC,
					onData:     m.onData,
				}
				m.AddConnection(connID, conn)
				delete(m.unestablishedConnections, connID)
			})
		}
	})

	go func() {
		answerGatheringComplete := webrtc.GatheringCompletePromise(pc)
		if err := pc.SetLocalDescription(answer); err != nil {
			log.Errorf("[%v]: Failed to set answer as local description for connection with id '%v' from peer '%v': %v", m.name, connID, src, err)
			return
		}
		<-answerGatheringComplete
		if err := m.iface.SendSDP(src, connID, &answer); err != nil {
			log.Errorf("[%v]: Failed to send answer for connection with id '%v' from peer '%v': %v", m.name, connID, src, err)
			return
		}
		atomic.StoreUint32(&answerSent, 1)
		// log.Debugf("[%v]: Sent answer SDP for connection with id '%v' from peer '%v': %v", n.id, connID, src, answer)

		for _, c := range pending {
			if err := m.iface.SendICECandidate(src, connID, c); err != nil {
				log.Errorf("[%v]: Failed to send ICE candidate for connection with id '%v' from peer '%v': %v", m.name, connID, src, err)
				return
			}
		}
	}()
	return nil
}

func (m *MaxP2P) OnAnswer(connID string, answer *webrtc.SessionDescription) error {
	var pc *webrtc.PeerConnection
	var err error
	func() {
		var ok bool
		m.Lock()
		defer m.Unlock()
		pc, ok = m.unestablishedConnections[connID]
		if !ok {
			err = fmt.Errorf("no connection with id '%v'", connID)
		}
	}()
	if err != nil {
		return err
	}
	log.Debugf("[%v]: Processing answer for connection with id '%v' from peer '%v'", m.name, connID, m.peer)
	return pc.SetRemoteDescription(*answer)
}

func (m *MaxP2P) OnICECandidate(connID string, candidate *webrtc.ICECandidateInit) error {
	var pc *webrtc.PeerConnection
	var err error
	func() {
		m.Lock()
		defer m.Unlock()
		conn, ok := m.connections[connID]
		if ok {
			pc = conn.pc
		} else {
			pc, ok = m.unestablishedConnections[connID]
			if !ok {
				err = fmt.Errorf("no connection with id '%v'", connID)
			}
		}
	}()
	if err != nil {
		return err
	}
	log.Debugf("[%v]: Adding incoming ICE candidate for connection with id '%v' from peer '%v'", m.name, connID, m.peer)
	return pc.AddICECandidate(*candidate)
}

func (m *MaxP2P) getID() string {
	id := atomic.AddUint32(&m.connID, 1)
	return fmt.Sprintf("%04d", id)
}

func (m *MaxP2P) newPeerConnection(peer string) (*P2PConn, error) {
	connID := m.getID()
	pc, err := m.api.NewPeerConnection(m.config)
	if err != nil {
		return nil, err
	}

	func() {
		m.Lock()
		defer m.Unlock()
		m.unestablishedConnections[connID] = pc
		m.pendingCandidates[connID] = make([]*webrtc.ICECandidate, 0)
	}()
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		desc := pc.RemoteDescription()
		if desc == nil {
			func() {
				m.Lock()
				defer m.Unlock()
				m.pendingCandidates[connID] = append(m.pendingCandidates[connID], c)
			}()
		} else {
			if err := m.iface.SendICECandidate(m.peer, connID, c); err != nil {
				log.Errorf("[%v]: Failed to send ICE candidate for connection with id '%v' to peer '%v': %v", m.name, connID, m.peer, err)
				return
			}
		}
	})
	// We need to connect this
	dc, err := pc.CreateDataChannel(fmt.Sprintf("%v-%v-data", peer, connID), nil)
	if err != nil {
		return nil, err
	}
	wg := sync.WaitGroup{}
	wg.Add(1)
	dc.OnOpen(func() {
		defer wg.Done()
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return nil, err
	}
	err = m.iface.SendOffer(peer, connID, &offer)
	if err != nil {
		return nil, err
	}
	offerGatheringComplete := webrtc.GatheringCompletePromise(pc)

	err = pc.SetLocalDescription(offer)
	if err != nil {
		return nil, err
	}

	<-offerGatheringComplete

	// Wait for datachannel to be opened
	wg.Wait()

	cdc, err := utils.NewCombinedDC(dc, m.maxBufferSize)
	if err != nil {
		log.Errorf("[%v]: Failed to create combinedDC for connection with id '%v' from peer '%v': %v", m.name, connID, peer, err)
		return nil, err
	}

	m.Lock()
	defer m.Unlock()
	conn := &P2PConn{
		pc:         pc,
		Mutex:      sync.Mutex{},
		combinedDC: cdc,
		onData:     m.onData,
	}
	m.AddConnection(connID, conn)
	return conn, nil
}

func (m *MaxP2P) AddConnection(connID string, conn *P2PConn) {
	m.connections[connID] = conn
	go m.handleWrites(conn)
	go m.handleReads(conn)
}

func (m *MaxP2P) handleWrites(conn *P2PConn) {
	encoder := m.serde.CreateEncoder(conn.combinedDC)

	globalWriteChan := m.writeChan
	var serialWriteChan chan network.WritePacket
	m.handleSerialWrites.Do(func() {
		serialWriteChan = m.serialWriteChan
	})

	encode := func(writePkt network.WritePacket) {
		err := encoder.Encode(writePkt.GetData())
		writePkt.GetCallback()(writePkt, err)
	}

	for {
		select {
		case writePkt, ok := <-globalWriteChan:
			if !ok {
				globalWriteChan = nil
				continue
			}
			encode(writePkt)
		case writePkt, ok := <-serialWriteChan:
			if !ok {
				serialWriteChan = nil
				continue
			}
			encode(writePkt)
		}
		if globalWriteChan == nil && serialWriteChan == nil {
			break
		}
	}
}

func (m *MaxP2P) handleReads(conn *P2PConn) error {
	err := m.chunkCombiner.AddReader(conn.combinedDC, fmt.Sprintf("combiner-%v", conn.combinedDC.Name))
	return err
}

func (m *MaxP2P) Send(data interface{}) error {
	return m._send(data)
}

func (m *MaxP2P) SendSerial(data interface{}) error {
	return m.chunkSplitter.SplitToChannel(data, m.serialWriteChan)
}

func (m *MaxP2P) _send(data interface{}) error {
	return m.chunkSplitter.Encode(data)
}
