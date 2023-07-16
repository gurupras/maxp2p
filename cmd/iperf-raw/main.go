package main

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	_ "net/http/pprof"

	"github.com/alecthomas/kingpin"
	"github.com/gurupras/maxp2p/v6/test_utils"
	"github.com/pion/webrtc/v3"
	"github.com/pkg/profile"
	log "github.com/sirupsen/logrus"
)

var (
	app               = kingpin.New("iperf", "Tool to measure bandwidth of maxp2p between two peers")
	name              = app.Flag("name", "Name of peer. Used to connect two peers").Short('n').String()
	signalServer      = app.Flag("signal-server", "Server address. Used for signaling between peers").Short('S').Required().String()
	verbose           = app.Flag("verbose", "Verbose logs").Short('v').Default("false").Bool()
	rawProfiles       = app.Flag("profile", "Profile the application. Valid options are cpu,memory,block,mutex").String()
	monitorGoroutines = app.Flag("grmon", "Monitor goroutines").Short('g').Default("0").Int()

	server = app.Command("server", "Run as 'server'. Waits for incoming connections")

	client         = app.Command("client", "Run as 'client'. Makes offer to server")
	numConnections = client.Flag("num-connections", "Number of connections to establish").Short('N').Default("30").Uint()
	rawPeer        = client.Flag("peer", "Peer to connect to").Short('p').Required().String()
	packetSize     = client.Flag("packet-size", "Per-packet size (3 bytes will be used for headers)").Default("4096").Int()
	duration       = client.Flag("duration", "Duration in seconds to transmit").Short('d').Default("10").Float64()
)

type pkt struct {
	Data []byte
}

// Mostly taken from https://github.com/dustin/go-humanize/blob/master/bytes.go
func Bytes(size float64) string {
	sizes := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	if size < 10 {
		return fmt.Sprintf("%v B", uint64(size))
	}
	e := math.Floor(math.Log(size) / math.Log(1000))
	suffix := sizes[int(e)]
	val := math.Floor(size/math.Pow(1000, e)*10+0.5) / 10
	return fmt.Sprintf("%.2f %s", val, suffix)
}

func main() {
	cmd := kingpin.MustParse(app.Parse(os.Args[1:]))

	if *verbose {
		log.SetLevel(log.DebugLevel)
	}

	var peer string = ""
	if rawPeer != nil {
		peer = *rawPeer
	}
	_ = peer

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create a SettingEngine
	s := webrtc.SettingEngine{}
	s.SetSCTPMaxReceiveBufferSize(100 * 1024 * 1024)

	// Create an API object with the engine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	node, err := test_utils.NewNode(*name, *signalServer, api, &config)
	if err != nil {
		log.Fatalf("Failed to setup peer: %v", err)
	}
	go node.HandleServerMessages()

	if *monitorGoroutines > 0 {
		addr := fmt.Sprintf(":%v", *monitorGoroutines)
		go http.ListenAndServe(addr, nil)
		log.Infof("Started http server on %v", addr)
	}

	if len(*rawProfiles) > 0 {
		*rawProfiles = strings.ReplaceAll(*rawProfiles, " ", ",")
		profiles := strings.Split(*rawProfiles, ",")
		log.Debugf("Enabling profiles: %v", profiles)
		profileFunctions := make([]func(p *profile.Profile), 0)
		if len(profiles) > 0 {
			profileFunctions = append(profileFunctions, profile.ProfilePath("."))
			for _, p := range profiles {
				if p == "cpu" {
					profileFunctions = append(profileFunctions, profile.CPUProfile)
				} else if p == "memory" {
					profileFunctions = append(profileFunctions, profile.MemProfile, profile.MemProfileRate(4096))
				} else if p == "block" {
					profileFunctions = append(profileFunctions, profile.BlockProfile)
				} else if p == "mutex" {
					profileFunctions = append(profileFunctions, profile.MutexProfile)
				}
			}
			defer profile.Start(profileFunctions...).Stop()
		}
	}

	computeThroughput := func(totalBytes, lastBytes uint64, durationSec float64) string {
		bps := Bytes(float64((totalBytes-lastBytes)*8) / durationSec)
		return bps
	}
	logThroughput := func(totalBytes, lastBytes uint64, durationSec float64) string {
		bps := computeThroughput(totalBytes, lastBytes, durationSec)
		ret := fmt.Sprintf("Throughput %v/s", bps)
		fmt.Printf("%v\n", ret)
		return ret
	}

	switch cmd {
	case server.FullCommand():
		{
			if *name == "" {
				*name = "server"
			}
			node.OnPeerConnection(func(pc *webrtc.PeerConnection) {
				received := uint64(0)
				var since time.Time
				lastBytes := uint64(0)

				pc.OnDataChannel(func(dc *webrtc.DataChannel) {
					dc.OnOpen(func() {
						dc.OnMessage(func(msg webrtc.DataChannelMessage) {
							b := msg.Data
							if received == 0 {
								since = time.Now()
							}
							received += uint64(len(b))
							now := time.Now()
							duration := now.Sub(since).Seconds()
							if duration > 2 {
								throughput := logThroughput(atomic.LoadUint64(&received), lastBytes, duration)
								since = now
								atomic.StoreUint64(&lastBytes, received)
								go func() {
									dc.Send([]byte(throughput))
								}()
							}
						})
					})
				})
			})
			c := make(chan struct{})
			<-c
		}
	}
}
