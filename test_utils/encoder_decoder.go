package test_utils

import (
	"io"

	"github.com/gurupras/maxp2p/types"
	"github.com/vmihailenco/msgpack"
)

type MsgpackSerDe struct{}

func (m *MsgpackSerDe) Marshal(data interface{}) ([]byte, error) {
	return msgpack.Marshal(data)
}

func (m *MsgpackSerDe) Unmarshal(data []byte, v interface{}) error {
	return msgpack.Unmarshal(data, v)
}

func (m *MsgpackSerDe) CreateEncoder(writer io.Writer) types.Encoder {
	return msgpack.NewEncoder(writer)
}

func (m *MsgpackSerDe) CreateDecoder(reader io.Reader) types.Decoder {
	return msgpack.NewDecoder(reader)
}
