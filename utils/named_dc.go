package utils

import (
	"fmt"
	"sync/atomic"

	"github.com/pion/webrtc/v3"
	log "github.com/sirupsen/logrus"
)

type NamedDC struct {
	name              string
	dc                *webrtc.DataChannel
	ch                chan struct{}
	counter           uint64
	maxBufferedAmount uint64
}

func NewNamedDC(dc *webrtc.DataChannel, maxBufferedAmount uint64, name string) *NamedDC {
	lowThreshold := maxBufferedAmount / 2
	log.Debugf("Buffered amount before setting low threshold: %v", dc.BufferedAmount())
	dc.SetBufferedAmountLowThreshold(lowThreshold)
	log.Debugf("[%v]: Set low threshold to %v", name, lowThreshold)
	log.Debugf("Buffered amount after setting low threshold: %v", dc.BufferedAmount())
	ret := &NamedDC{
		name:              name,
		dc:                dc,
		ch:                make(chan struct{}),
		counter:           0,
		maxBufferedAmount: maxBufferedAmount,
	}

	dc.OnBufferedAmountLow(func() {
		// log.Debugf("[%v]: Buffered amount low", ret.name)
		// newVal := atomic.AddUint64(&ret.counter, 1)
		// log.Debugf("[%v]: Attempting to notify writable (counter=%v)", ret.name, newVal)
		select {
		case ret.ch <- struct{}{}:
		default:
		}
		// log.Debugf("[%v]: Notified writable (counter=%v)", ret.name, newVal)
	})
	return ret
}

func (dc *NamedDC) Write(b []byte) (int, error) {
	if len(b) > int(dc.maxBufferedAmount) {
		return 0, fmt.Errorf("attempting to write data greater than maxBufferedAmount (%v > %v). Increase maxBufferedAmount to support this", len(b), dc.maxBufferedAmount)
	}
	err := dc.dc.Send(b)
	if err != nil {
		return 0, err
	}
	bufferedAmount := dc.dc.BufferedAmount()
	if bufferedAmount > dc.maxBufferedAmount {
		val := atomic.LoadUint64(&dc.counter)
		log.Debugf("[%v]: Waiting for drain (bufferedAmount=%v) write=%v total=%v > %v (counter=%v)", dc.name, bufferedAmount, len(b), bufferedAmount, dc.maxBufferedAmount, val+1)
		<-dc.ch
		log.Debugf("[%v]: Finished waiting for drain", dc.name)
	}
	return len(b), nil
}

func (dc *NamedDC) Close() error {
	return dc.dc.Close()
}

func (dc *NamedDC) Name() string {
	return dc.name
}

func (dc *NamedDC) DataChannel() *webrtc.DataChannel {
	return dc.dc
}
