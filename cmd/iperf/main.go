package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/gurupras/maxp2p"
	"github.com/gurupras/maxp2p/test_utils"
	"github.com/gurupras/maxp2p/types"
	"github.com/pion/webrtc/v3"
	"github.com/pkg/profile"
	log "github.com/sirupsen/logrus"
)

var (
	app          = kingpin.New("iperf", "Tool to measure bandwidth of maxp2p between two peers")
	name         = app.Flag("name", "Name of peer. Used to connect two peers").Short('n').String()
	signalServer = app.Flag("signal-server", "Server address. Used for signaling between peers").Short('S').Required().String()
	verbose      = app.Flag("verbose", "Verbose logs").Short('v').Default("false").Bool()
	rawProfiles  = app.Flag("profile", "Profile the application. Valid options are cpu,memory,block,mutex").String()

	server = app.Command("server", "Run as 'server'. Waits for incoming connections")

	client         = app.Command("client", "Run as 'client'. Makes offer to server")
	numConnections = client.Flag("num-connections", "Number of connections to establish").Short('N').Default("30").Uint()
	rawPeer        = client.Flag("peer", "Peer to connect to").Short('p').Required().String()
	packetSize     = client.Flag("packet-size", "Per-packet size (3 bytes will be used for headers)").Default("4096").Int()
	duration       = client.Flag("duration", "Duration in seconds to transmit").Short('d').Default("10").Float64()
)

type serde struct {
	*test_utils.LengthSerDe
}
type plainEncoder struct {
	io.Writer
}

func (s *serde) CreateEncoder(writer io.Writer) types.Encoder {
	return &plainEncoder{Writer: writer}
}

func (p *plainEncoder) Encode(v interface{}) error {
	b, ok := v.([]byte)
	if !ok {
		log.Fatalf("Failed to convert interface to []byte")
	}
	_, err := p.Write(b)
	if err != nil {
		log.Fatalf("Error while encoding data: %v", err)
	} else {
		log.Debugf("Wrote %v bytes", len(b))
	}
	return err
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

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// Create a SettingEngine and enable Detach
	s := webrtc.SettingEngine{}
	s.SetSCTPMaxReceiveBufferSize(100 * 1024 * 1024)
	s.DetachDataChannels()

	// Create an API object with the engine
	api := webrtc.NewAPI(webrtc.WithSettingEngine(s))

	node, err := test_utils.NewNode(*name, *signalServer, api, &config)
	if err != nil {
		log.Fatalf("Failed to setup peer: %v", err)
	}
	go node.HandleServerMessages()
	mNode := test_utils.MaxP2PTestNode{Node: node}

	var serde test_utils.LengthSerDe

	mp2p, err := maxp2p.New(*name, peer, &mNode, &serde, config, 100*1024*1024)
	if err != nil {
		log.Fatalf("Failed to create maxp2p: %v", err)
	}

	node.OnOffer(func(src, connID string, offer *webrtc.SessionDescription) {
		mp2p.OnOffer(src, connID, offer)
	})
	node.OnSDP(func(src, connID string, sdp *webrtc.SessionDescription) {
		mp2p.OnAnswer(connID, sdp)
	})
	node.OnICECandidate(func(src, connID string, c *webrtc.ICECandidateInit) {
		mp2p.OnICECandidate(connID, c)
	})

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
					profileFunctions = append(profileFunctions, profile.MemProfile)
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
	logThroughput := func(totalBytes, lastBytes uint64, durationSec float64) {
		bps := computeThroughput(totalBytes, lastBytes, durationSec)
		fmt.Printf("Throughput %v/s\n", bps)
	}

	switch cmd {
	case client.FullCommand():
		{
			if *name == "" {
				*name = "client"
			}

			mp2p.Start(int(*numConnections))
			// We will write packets of size 65532 (msgpack takes 3 extra bytes)
			writeBytes := make([]byte, *packetSize)
			rand.Read(writeBytes)
			encodedBytes := writeBytes

			log.Debugf("Going to write %v bytes every time", len(encodedBytes))

			sent := uint64(0)
			lastBytes := uint64(0)

			var durationSec float64
			start := time.Now()
			since := start

			for {
				now := time.Now()
				durationSec = now.Sub(start).Seconds()
				if durationSec >= *duration {
					break
				}
				sinceLast := now.Sub(since).Seconds()
				if sinceLast > 2 {
					logThroughput(atomic.LoadUint64(&sent), lastBytes, sinceLast)
					since = now
					lastBytes = sent
				}
				err = mp2p.Send(encodedBytes)
				if err != nil {
					log.Fatalf("Failed to send bytes to peer: %v", err)
				}
				sent += uint64(len(writeBytes))
			}
			fmt.Printf("Transferred %vytes (%v/s)\n", Bytes(float64(sent)), computeThroughput(sent, 0, durationSec))
		}
	case server.FullCommand():
		{
			if *name == "" {
				*name = "server"
			}
			received := uint64(0)
			since := time.Now()
			lastBytes := uint64(0)
			mp2p.OnData(func(data interface{}) {
				b := data.([]byte)
				received += uint64(len(b))
				now := time.Now()
				duration := now.Sub(since).Seconds()
				if duration > 2 {
					logThroughput(atomic.LoadUint64(&received), lastBytes, duration)
					since = now
					atomic.StoreUint64(&lastBytes, received)
				}
			})
			c := make(chan struct{})
			<-c
		}
	}
}
