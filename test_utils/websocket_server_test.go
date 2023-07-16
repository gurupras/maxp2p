package test_utils

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/gurupras/maxp2p/v5/types"
	"github.com/gurupras/maxp2p/v5/utils"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestWebSocketServerSetupBackend(t *testing.T) {
	require := require.New(t)

	backend, err := SetupBackend()
	require.Nil(err)
	require.NotNil(backend)
}

func TestWebSocketServerForward(t *testing.T) {
	require := require.New(t)

	backend, _ := SetupBackend()

	d1 := "device-1"
	d2 := "device-2"
	data := "{}"

	u1 := utils.CreateDeviceIDQuery(d1, backend.URL)
	u2 := utils.CreateDeviceIDQuery(d2, backend.URL)

	log.Debugf("u1: %v\n", u1)
	log.Debugf("u2: %v\n", u2)

	ws1, _, err := websocket.DefaultDialer.Dial(u1, nil)
	require.Nil(err)
	// Wait for ready
	var m map[string]interface{}
	err = ws1.ReadJSON(&m)
	require.Nil(err)
	require.Equal("ready", m["action"].(string))
	delete(m, "action")

	ws2, _, err := websocket.DefaultDialer.Dial(u2, nil)
	require.Nil(err)
	// Wait for ready
	err = ws2.ReadJSON(&m)
	require.Nil(err)
	require.Equal("ready", m["action"].(string))

	// Send a packet on ws1 and expect it to arrive on ws2
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		_, message, err := ws2.ReadMessage()
		require.Nil(err)
		var m map[string]interface{}
		err = json.Unmarshal(message, &m)
		require.Nil(err)
		require.Equal(m["dest"], d2)
		require.Equal(m["data"], data)
	}()
	pkt := types.SignalPacket{
		Packet: &types.Packet{
			Type: types.CandidatePacketType,
			Data: data,
		},
		Src:  d1,
		Dest: d2,
	}
	err = ws1.WriteJSON(pkt)
	require.Nil(err)
	wg.Wait()
	backend.Shutdown()
}
