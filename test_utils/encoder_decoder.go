package test_utils

import (
	"encoding/binary"
	"io"

	"github.com/gurupras/maxp2p/types"
)

type LengthSerDe struct{}

type lengthEncoder struct {
	io.Writer
	lenBytes []byte
}

func (l *lengthEncoder) Encode(data interface{}) error {
	b := data.([]byte)
	binary.LittleEndian.PutUint64(l.lenBytes, uint64(len(b)))
	_, err := l.Writer.Write(l.lenBytes)
	if err != nil {
		return err
	}
	_, err = l.Writer.Write(b)
	return err
}

type lengthDecoder struct {
	io.Reader
	lenBytes []byte
}

func (l *lengthDecoder) Decode() (interface{}, error) {
	_, err := io.ReadFull(l.Reader, l.lenBytes)
	if err != nil {
		return nil, err
	}
	n := int(binary.LittleEndian.Uint64(l.lenBytes))
	buf := make([]byte, n)
	_, err = io.ReadFull(l.Reader, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (m *LengthSerDe) CreateEncoder(writer io.Writer) types.Encoder {
	return &lengthEncoder{
		Writer:   writer,
		lenBytes: make([]byte, 8),
	}
}

func (m *LengthSerDe) CreateDecoder(reader io.Reader) types.Decoder {
	return &lengthDecoder{
		Reader:   reader,
		lenBytes: make([]byte, 8),
	}
}
