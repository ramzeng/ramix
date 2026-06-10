package ramix

import (
	"encoding/binary"
	"fmt"
	"math"
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
	if len(data) < d.headerSize() {
		return Message{}, fmt.Errorf("%w: frame too short: got %d bytes, need at least %d", ErrInvalidFrame, len(data), d.headerSize())
	}

	message := Message{
		Event:    binary.LittleEndian.Uint32(data[0:4]),
		BodySize: binary.LittleEndian.Uint32(data[4:8]),
	}

	actualBodySize := uint64(len(data) - d.headerSize())
	if err := validateDecodedBodySize(message.BodySize, actualBodySize); err != nil {
		return Message{}, err
	}

	message.Body = data[d.headerSize():]

	return message, nil
}

func validateDecodedBodySize(declared uint32, actual uint64) error {
	if actual > math.MaxUint32 {
		return fmt.Errorf("%w: actual body length %d exceeds uint32 max %d", ErrInvalidFrame, actual, uint64(math.MaxUint32))
	}

	if uint64(declared) != actual {
		return fmt.Errorf("%w: declared body length %d does not match actual %d", ErrInvalidFrame, declared, actual)
	}

	return nil
}
