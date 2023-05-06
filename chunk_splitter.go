package maxp2p

import (
	"fmt"
	"math"
	"sync"

	"github.com/gurupras/maxp2p/v2/types"
)

type ChunkSplitter struct {
	name            string
	MaxPacketSize   int
	marshaler       types.Marshaler
	outChan         chan<- *writePacket
	writePacketPool *sync.Pool
	packetIdx       uint64
}

func NewChunkSplitter(name string, maxPacketSize int, marshaler types.Marshaler, outChan chan<- *writePacket) *ChunkSplitter {
	writePacketPool := &sync.Pool{
		New: func() any {
			return &writePacket{}
		},
	}

	return &ChunkSplitter{
		name:            name,
		MaxPacketSize:   maxPacketSize,
		marshaler:       marshaler,
		outChan:         outChan,
		writePacketPool: writePacketPool,
		packetIdx:       0,
	}
}

func (c *ChunkSplitter) Encode(v interface{}) error {
	encodedBytes, err := c.marshaler.Marshal(v)
	if err != nil {
		return err
	}
	numChunks := int(math.Ceil(float64(len(encodedBytes)) / float64(c.MaxPacketSize)))
	packetID := c.packetIdx
	c.packetIdx += 1
	written := 0

	wg := sync.WaitGroup{}
	wg.Add(numChunks)

	mutex := sync.Mutex{}
	errors := make([]error, 0)

	callback := func(writePkt *writePacket, e error) {
		defer wg.Done()
		c.writePacketPool.Put(writePkt)
		if e != nil {
			mutex.Lock()
			defer mutex.Unlock()
			errors = append(errors, e)
		}
	}

	for chunkIdx := 0; chunkIdx < numChunks; chunkIdx++ {
		remaining := len(encodedBytes) - written
		chunkSize := int(math.Min(float64(remaining), float64(c.MaxPacketSize)))
		chunk := &types.Chunk{
			ID:   packetID,
			Seq:  uint64(chunkIdx),
			End:  chunkIdx == numChunks-1,
			Data: encodedBytes[written : written+chunkSize],
		}
		writePkt := c.writePacketPool.Get().(*writePacket)
		writePkt.data = chunk
		writePkt.cb = callback
		c.outChan <- writePkt
		func() {
			mutex.Lock()
			defer mutex.Unlock()
			written += chunkSize
		}()
	}
	wg.Wait()
	if len(errors) != 0 {
		err := fmt.Errorf("[%v]: Encountered errors when encoding: %v", c.name, errors[0])
		return err
	}
	return nil
}
