package maxp2p

import (
	"io"
	"sync"
	"testing"

	"github.com/gurupras/go-network"
	"github.com/gurupras/maxp2p/v5/test_utils"
	"github.com/gurupras/maxp2p/v5/utils"
	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type pkt struct {
	Data interface{}
}

type maxP2PTest struct {
	suite.Suite
	backend *test_utils.Backend
	api     *webrtc.API
	config  webrtc.Configuration
	p1      *test_utils.Node
	p2      *test_utils.Node
	maxp2p1 *MaxP2P
	maxp2p2 *MaxP2P
}

func (m *maxP2PTest) SetupTest() {
	require := require.New(m.T())
	backend, err := test_utils.SetupBackend()
	require.Nil(err)
	m.backend = backend

	m.config = webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create a SettingEngine
	s := webrtc.SettingEngine{}

	// Create an API object with the engine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))
	m.api = api

	d1 := "device-1"
	d2 := "device-2"

	m.p1, err = test_utils.NewNode(d1, backend.URL, api, &m.config)
	require.Nil(err)
	go m.p1.HandleServerMessages()

	m.p2, err = test_utils.NewNode(d2, backend.URL, api, &m.config)
	require.Nil(err)
	go m.p2.HandleServerMessages()

	n1 := &test_utils.MaxP2PTestNode{Node: m.p1}

	var serde test_utils.MsgpackSerDe
	m.maxp2p1, err = New(d1, d2, n1, &serde, func() interface{} {
		return &pkt{}
	}, m.config, 1*1024*1024)
	require.Nil(err)

	m.p1.OnICECandidate(func(src, connID string, c *webrtc.ICECandidateInit) {
		m.maxp2p1.OnICECandidate(connID, c)
	})
	m.p1.OnSDP(func(src, connID string, sdp *webrtc.SessionDescription) {
		m.maxp2p1.OnSDP(src, connID, sdp)
	})
}

func (m *maxP2PTest) TearDownTest() {
	if m.maxp2p2 != nil {
		m.maxp2p2.Close()
	}
	m.maxp2p1.Close()
	m.p1.Close()
	m.p2.Close()
	m.backend.Server.Close()
}

func (m *maxP2PTest) TestStart() {
	// m.T().Skip()
	require := require.New(m.T())

	m.maxp2p1.Start()
	require.Greater(len(m.maxp2p1.connections), 0)
}

func (m *maxP2PTest) testWrite(expected []byte) {
	require := require.New(m.T())

	var read func() []byte
	readReadyChan := make(chan struct{}, 1)

	if m.maxp2p2 != nil {
		wg := sync.WaitGroup{}
		wg.Add(1)
		once := sync.Once{}
		read = func() []byte {
			var ret []byte
			m.maxp2p2.OnData(func(data interface{}, discard func()) {
				once.Do(func() {
					defer wg.Done()
					pkt := data.(*pkt)
					ret = pkt.Data.([]byte)
					discard()
				})
			})
			wg.Wait()
			return ret
		}
		readReadyChan <- struct{}{}
	} else {
		serde := &test_utils.MsgpackSerDe{}
		outChan := make(chan io.Reader)
		chunkCombiner := network.NewChunkCombiner("pc", serde, outChan)

		onPeerConnection := func(pc *webrtc.PeerConnection) {
			pc.OnDataChannel(func(dc *webrtc.DataChannel) {
				dc.OnOpen(func() {
					dc.OnMessage(func(msg webrtc.DataChannelMessage) {
						err := chunkCombiner.ProcessIncomingBytes(msg.Data)
						require.Nil(err)
					})
					read = func() []byte {
						reader := <-outChan
						decoder := serde.CreateDecoder(reader)
						pkt := &pkt{}
						err := decoder.Decode(pkt)
						require.Nil(err)
						return pkt.Data.([]byte)
					}
					readReadyChan <- struct{}{}
				})
			})
		}
		m.p2.OnPeerConnection(onPeerConnection)
	}

	m.maxp2p1.Start()

	pkt := &pkt{Data: expected}
	err := m.maxp2p1.Send(pkt)
	require.Nil(err)

	<-readReadyChan
	got := read()

	require.Equal(expected, got)
}

func (m *maxP2PTest) TestWriteSmall() {
	// m.T().Skip()
	m.testWrite([]byte("hello"))
}

func (m *maxP2PTest) TestWriteLarge() {
	// m.T().Skip()
	randString := test_utils.RandomString(128 * 1024)
	m.testWrite([]byte(randString))
}

func (m *maxP2PTest) TestRead() {
	// m.T().Skip()
	require := require.New(m.T())

	wg := sync.WaitGroup{}
	wg.Add(1)
	once := sync.Once{}

	var send func(v interface{}) error
	sendReadyChan := make(chan struct{}, 1)

	expected := []byte("hello")
	var got []byte

	writePktChan := make(chan network.WritePacket)
	defer close(writePktChan)

	onPeerConnection := func(pc *webrtc.PeerConnection) {
		if m.maxp2p2 != nil {
			once.Do(func() {
				defer wg.Done()
				send = func(v interface{}) error {
					return m.maxp2p2.Send(v)
				}
				sendReadyChan <- struct{}{}
			})
		} else {
			pc.OnDataChannel(func(dc *webrtc.DataChannel) {
				dc.OnOpen(func() {
					once.Do(func() {
						defer wg.Done()
						serde := &test_utils.MsgpackSerDe{}
						combinedDC := utils.NewNamedDC(dc, 1*1024*1024, dc.Label())
						chunkSplitter := network.NewChunkSplitter("pc", MaxPacketSize-64, serde, writePktChan)
						go func() {
							for writePkt := range writePktChan {
								b, err := serde.Marshal(writePkt.GetData())
								require.Nil(err)
								err = combinedDC.DataChannel().Send(b)
								writePkt.GetCallback()(writePkt, err)
								require.Nil(err)
							}
						}()

						send = func(v interface{}) error {
							return chunkSplitter.Encode(v)
						}
						sendReadyChan <- struct{}{}
					})
				})
			})
		}
	}

	if m.maxp2p2 != nil {
		m.maxp2p2.onPeerConnection = onPeerConnection
	} else {
		m.p2.OnPeerConnection(onPeerConnection)
	}

	err := m.maxp2p1.Start()
	require.Nil(err)

	wg.Wait()

	wg = sync.WaitGroup{}
	wg.Add(1)

	m.maxp2p1.OnData(func(data interface{}, discard func()) {
		defer wg.Done()
		pkt := data.(*pkt)
		got = pkt.Data.([]byte)
	})

	<-sendReadyChan

	pkt := &pkt{
		Data: expected,
	}
	err = send(pkt)
	require.Nil(err)

	wg.Wait()
	require.Equal(expected, got)
}

func TestMaxP2PWithNonMaxP2P(t *testing.T) {
	// log.SetLevel(log.DebugLevel)
	suite.Run(t, new(maxP2PTest))
}

type maxP2PWithMaxP2P struct {
	maxP2PTest
}

func (m *maxP2PWithMaxP2P) SetupTest() {
	require := require.New(m.T())

	var err error

	m.maxP2PTest.SetupTest()

	// Make p2 into a maxP2P node
	n2 := &test_utils.MaxP2PTestNode{Node: m.p2}

	var serde test_utils.MsgpackSerDe
	m.maxp2p2, err = New(m.p2.ID, m.p1.ID, n2, &serde, func() interface{} {
		return &pkt{}
	}, m.config, 1*1024*1024)
	require.Nil(err)

	m.p2.OnICECandidate(func(src, connID string, c *webrtc.ICECandidateInit) {
		m.maxp2p2.OnICECandidate(connID, c)
	})
	m.p2.OnSDP(func(src, connID string, sdp *webrtc.SessionDescription) {
		m.maxp2p2.OnSDP(src, connID, sdp)
	})
}

func TestMaxP2PWithMaxP2P(t *testing.T) {
	// log.SetLevel(log.DebugLevel)
	suite.Run(t, new(maxP2PWithMaxP2P))
}
