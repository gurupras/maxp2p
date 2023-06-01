package test_utils

import (
	"fmt"
	"sync"
	"testing"

	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestWebRTCConnection(t *testing.T) {
	require := require.New(t)

	d1 := "device-1"
	d2 := "device-2"

	backend, err := SetupBackend()
	require.Nil(err)
	urlStr := backend.URL

	webrtcSettings := webrtc.SettingEngine{}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(webrtcSettings))

	config := webrtc.Configuration{}

	node1, err := NewNode(d1, urlStr, api, &config)
	require.Nil(err)
	go node1.HandleServerMessages()

	node2, err := NewNode(d2, urlStr, api, &config)
	require.Nil(err)
	go node2.HandleServerMessages()

	// We want to bump up the SDP packet sizes
	numChannels := 10

	wg1 := sync.WaitGroup{}
	wg1.Add(numChannels)
	// Conn1 does offer
	err = node1.Offer(d2, func(pc *webrtc.PeerConnection) {
		for idx := 0; idx < numChannels; idx++ {
			dc1, err := pc.CreateDataChannel(fmt.Sprintf("data-%v", idx), nil)
			require.Nil(err)
			dc1.OnOpen(func() {
				wg1.Done()
			})
		}
	})
	require.Nil(err)

	wg2 := sync.WaitGroup{}
	wg2.Add(numChannels)

	node2.OnPeerConnection(func(pc *webrtc.PeerConnection) {
		pc.OnDataChannel(func(d *webrtc.DataChannel) {
			d.OnOpen(func() {
				wg2.Done()
			})
		})
	})

	wg1.Wait()
	wg2.Wait()

	node1.Close()
	node2.Close()
}

func TestCreateChannelAfterConnection(t *testing.T) {
	require := require.New(t)

	d1 := "device-1"
	d2 := "device-2"

	backend, err := SetupBackend()
	require.Nil(err)
	urlStr := backend.URL

	webrtcSettings := webrtc.SettingEngine{}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(webrtcSettings))

	config := webrtc.Configuration{}

	node1, err := NewNode(d1, urlStr, api, &config)
	require.Nil(err)
	go node1.HandleServerMessages()

	node2, err := NewNode(d2, urlStr, api, &config)
	require.Nil(err)
	go node2.HandleServerMessages()

	wg1 := sync.WaitGroup{}
	wg1.Add(1)

	wg2 := sync.WaitGroup{}
	wg2.Add(1)

	var (
		pc1 *webrtc.PeerConnection
		pc2 *webrtc.PeerConnection
	)

	node2.OnPeerConnection(func(pc *webrtc.PeerConnection) {
		pc2 = pc
		pc.OnDataChannel(func(dc *webrtc.DataChannel) {
			dc.OnOpen(func() {
				defer wg2.Done()
			})
		})
	})

	// Conn1 does offer
	err = node1.Offer(d2, func(pc *webrtc.PeerConnection) {
		pc1 = pc
		dc1, err := pc.CreateDataChannel("channel", nil)
		require.Nil(err)
		dc1.OnOpen(func() {
			defer wg1.Done()
		})
	})
	require.Nil(err)

	wg1.Wait()
	wg2.Wait()

	// Now, try to create another data channel
	numChannels := 300
	wg1 = sync.WaitGroup{}
	wg1.Add(numChannels * 2)
	pc2.OnDataChannel(func(dc *webrtc.DataChannel) {
		go func() {
			dc.OnOpen(func() {
				defer wg1.Done()
			})
		}()
	})

	for idx := 0; idx < numChannels; idx++ {
		dc, err := pc1.CreateDataChannel(gonanoid.Must(), nil)
		require.Nil(err)
		go func(dc *webrtc.DataChannel) {
			log.Debugf("[%v]: Waiting for open", d1)
			dc.OnOpen(func() {
				defer wg1.Done()
				log.Debugf("[%v]: Done waiting for open", d1)
			})
		}(dc)
	}

	wg1.Wait()
	// Try sending/receiving messages

	node1.Close()
	node2.Close()
}
