package ramix

import (
	"encoding/binary"
	"fmt"
	"math"
)

type EncoderInterface interface {
	Encode(Message) ([]byte, error)
}

type Encoder struct {
}

func (d *Encoder) Encode(message Message) ([]byte, error) {
	bodyLength := len(message.Body)
	if err := validateEncodedBodyLength(uint64(bodyLength), maxEncodedBodyLength()); err != nil {
		return nil, err
	}

	encoded := make([]byte, 8+bodyLength)
	binary.LittleEndian.PutUint32(encoded[0:4], message.Event)
	binary.LittleEndian.PutUint32(encoded[4:8], uint32(bodyLength))
	copy(encoded[8:], message.Body)

	return encoded, nil
}

func validateEncodedBodyLength(bodyLength uint64, maxBodyLength uint64) error {
	if bodyLength > maxBodyLength {
		return fmt.Errorf("%w: body length %d exceeds supported maximum %d", ErrInvalidFrame, bodyLength, maxBodyLength)
	}

	return nil
}

func maxEncodedBodyLength() uint64 {
	maxInt := int(^uint(0) >> 1)
	if maxInt < 8 {
		return 0
	}

	allocationLimit := uint64(maxInt - 8)
	if allocationLimit > uint64(math.MaxUint32) {
		return uint64(math.MaxUint32)
	}

	return allocationLimit
}
