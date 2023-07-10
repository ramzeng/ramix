package ramix

import (
	"bytes"
	"encoding/binary"
)

type EncoderInterface interface {
	Encode(Message) ([]byte, error)
}

type Encoder struct {
}

func (d *Encoder) Encode(message Message) ([]byte, error) {
	buffer := bytes.NewBuffer([]byte{})

	if err := binary.Write(buffer, binary.LittleEndian, message.Event); err != nil {
		return nil, err
	}

	if err := binary.Write(buffer, binary.LittleEndian, message.BodySize); err != nil {
		return nil, err
	}

	if err := binary.Write(buffer, binary.LittleEndian, message.Body); err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}
