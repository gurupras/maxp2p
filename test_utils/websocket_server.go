package test_utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gurupras/maxp2p/v6/types"
	log "github.com/sirupsen/logrus"
)

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	log.Debugf("Returning port: %v\n", port)
	return port, nil
}

var upgrader = websocket.Upgrader{}

func (b *Backend) forward(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("deviceID")
	if deviceID == "" {
		log.Errorf("Websocket connection with no deviceID")
		return
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
	defer c.Close()
	b.mutex.Lock()
	b.DeviceMap[deviceID] = c
	b.mutex.Unlock()

	c.SetCloseHandler(func(code int, text string) error {
		log.Warnf("[%v] websocket connection closed", deviceID)
		b.mutex.Lock()
		defer b.mutex.Unlock()
		delete(b.DeviceMap, deviceID)
		return nil
	})

	if err = c.WriteJSON(map[string]interface{}{
		"action": "ready",
	}); err != nil {
		log.Errorf("Failed to send ready signal to websocket: %v\n", err)
		return
	}

	for {
		mt, message, err := c.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
				log.Errorf("[%v]: Failed to read message: %v\n", deviceID, err)
			}
			break
		}
		pkt := types.SignalPacket{}
		err = json.Unmarshal(message, &pkt)
		if err != nil {
			log.Errorf("[%v]: Failed to parse JSON from message: %v\n", deviceID, err)
			break
		}
		dest := pkt.Dest
		b.mutex.Lock()
		destConn, ok := b.DeviceMap[dest]
		b.mutex.Unlock()
		if !ok {
			log.Errorf("[%v]: No connection found for destination: %v\n", deviceID, dest)
			continue
		}
		if deviceID == dest {
			log.Warnf("Forwarding message: %v --> %v (%v)", deviceID, dest, string(message))
		}
		err = destConn.WriteMessage(mt, message)
		if err != nil {
			log.Errorf("[%v]: write to destination (%v) failed: %v\n", deviceID, dest, err)
			break
		}
	}
}

func (b *Backend) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := b.Server.Shutdown(ctx)
	b.wg.Wait()
	return err
}

type Backend struct {
	Server    *http.Server
	wg        sync.WaitGroup
	mutex     sync.Mutex
	URL       string
	DeviceMap map[string]*websocket.Conn
}

func SetupBackend(ports ...int) (*Backend, error) {
	var err error
	var port int
	if (len(ports)) == 0 {
		port, err = getFreePort()
		if err != nil {
			return nil, err
		}
	} else {
		port = ports[0]
	}
	server := http.NewServeMux()
	host := fmt.Sprintf("0.0.0.0:%v", port)
	url := fmt.Sprintf("ws://%v/ws", host)
	log.Debugf("url: %v\n", url)
	res := &Backend{
		Server: &http.Server{
			Addr:    host,
			Handler: server,
		},
		wg:        sync.WaitGroup{},
		mutex:     sync.Mutex{},
		URL:       url,
		DeviceMap: make(map[string]*websocket.Conn),
	}
	res.wg.Add(1)
	server.HandleFunc("/ws", res.forward)

	listener, err := net.Listen("tcp", host)
	if err != nil {
		return nil, err
	}
	go func(b *Backend, listener net.Listener) {
		defer b.wg.Done()
		b.Server.Serve(listener)
		log.Debugf("Backend server terminated")
	}(res, listener)
	return res, nil
}
