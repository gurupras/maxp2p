package utils

import (
	"fmt"
	"sync/atomic"

	"github.com/pion/datachannel"
	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

type CombinedDC struct {
	name              string
	dc                *webrtc.DataChannel
	raw               datachannel.ReadWriteCloser
	ch                chan struct{}
	counter           uint64
	maxBufferedAmount uint64
}

func NewCombinedDC(dc *webrtc.DataChannel, maxBufferedAmount uint64) (*CombinedDC, error) {
	lowThreshold := maxBufferedAmount / 2
	log.Debugf("Buffered amount before setting low threshold: %v", dc.BufferedAmount())
	dc.SetBufferedAmountLowThreshold(lowThreshold)
	log.Debugf("[%v]: Set low threshold to %v", dc.Label(), lowThreshold)
	log.Debugf("Buffered amount after setting low threshold: %v", dc.BufferedAmount())

	raw, err := dc.Detach()
	if err != nil {
		return nil, err
	}
	ret := &CombinedDC{
		name:              dc.Label(),
		dc:                dc,
		raw:               raw,
		ch:                make(chan struct{}, 1),
		counter:           0,
		maxBufferedAmount: maxBufferedAmount,
	}
	dc.OnBufferedAmountLow(func() {
		log.Debugf("[%v]: Buffered amount low", ret.name)
		newVal := atomic.AddUint64(&ret.counter, 1)
		log.Debugf("[%v]: Attempting to notify writable (counter=%v)", ret.name, newVal)
		ret.ch <- struct{}{}
		log.Debugf("[%v]: Notified writable (counter=%v)", ret.name, newVal)
	})
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

func (dc *CombinedDC) Close() error {
	return dc.dc.Close()
}
