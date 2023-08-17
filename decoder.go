package ramix

import (
	"encoding/binary"
	"fmt"
)

type DecoderInterface interface {
	Decode([]byte) (Message, error)
}

type Decoder struct {
}

func (d *Decoder) headerSize() int {
	return 8
}

func (d *Decoder) Decode(data []byte) (Message, error) {
	message := Message{}

	message.Event = binary.LittleEndian.Uint32(data[0:4])

	message.BodySize = binary.LittleEndian.Uint32(data[4:8])

	if message.BodySize > 0 && len(data) < int(message.BodySize)+d.headerSize() {
		return message, fmt.Errorf("message size %d exceeds data size %d", message.BodySize, len(data))
	}

	message.Body = data[d.headerSize() : d.headerSize()+int(message.BodySize)]

	return message, nil
}
