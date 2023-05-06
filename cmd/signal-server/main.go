package main

import (
	"github.com/alecthomas/kingpin"
	"github.com/gurupras/maxp2p/v2/test_utils"
	log "github.com/sirupsen/logrus"
)

var (
	port = kingpin.Flag("port", "Port to listen on").Short('p').Default("3330").Int()
)

func main() {
	kingpin.Parse()

	backend, err := test_utils.SetupBackend(*port)
	if err != nil {
		log.Fatalf("Failed to set up server: %v", err)
	}

	log.Infof("Server listening on port: %v", *port)
	log.Infof("Use the following URL to connect: %v", backend.URL)

	c := make(chan struct{})
	<-c
}
