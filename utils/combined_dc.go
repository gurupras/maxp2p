package utils

import (
	"fmt"
	"sync/atomic"

	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

type CombinedDC struct {
	*NamedDC
	raw datachannel.ReadWriteCloser
}

func NewCombinedDC(dc *webrtc.DataChannel, maxBufferedAmount uint64) (*CombinedDC, error) {
	namedDC := NewNamedDC(dc, maxBufferedAmount, fmt.Sprintf("combined-dc-%v", dc.Label()))
	raw, err := dc.Detach()
	if err != nil {
		return nil, err
	}
	ret := &CombinedDC{
		NamedDC: namedDC,
		raw:     raw,
	}

	return ret, nil
}

func (dc *CombinedDC) Read(b []byte) (int, error) {
	return dc.raw.Read(b)
}

func (dc *CombinedDC) Write(b []byte) (int, error) {
	if len(b) > int(dc.maxBufferedAmount) {
		return 0, fmt.Errorf("attempting to write data greater than maxBufferedAmount (%v > %v). Increase maxBufferedAmount to support this", len(b), dc.maxBufferedAmount)
	}
	n, err := dc.raw.Write(b)
	if err != nil {
		return n, err
	}
	bufferedAmount := dc.dc.BufferedAmount()
	if bufferedAmount > dc.maxBufferedAmount {
		val := atomic.LoadUint64(&dc.counter)
		log.Debugf("[%v]: Waiting for drain (bufferedAmount=%v) write=%v total=%v > %v (counter=%v)", dc.name, bufferedAmount, len(b), bufferedAmount, dc.maxBufferedAmount, val+1)
		<-dc.ch
		log.Debugf("[%v]: Finished waiting for drain", dc.name)
	}
	return n, err
}
