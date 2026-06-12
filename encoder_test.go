package ramix

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestEncoderIgnoresSuppliedBodySize(t *testing.T) {
	t.Parallel()

	encoded, err := (&Encoder{}).Encode(Message{
		Event:    7,
		BodySize: 999,
		Body:     []byte("ok"),
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	if got, want := binary.LittleEndian.Uint32(encoded[0:4]), uint32(7); got != want {
		t.Fatalf("event = %d, want %d", got, want)
	}
	if got, want := binary.LittleEndian.Uint32(encoded[4:8]), uint32(2); got != want {
		t.Fatalf("body size = %d, want %d", got, want)
	}
	if got, want := string(encoded[8:]), "ok"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestEncoderRoundTrip(t *testing.T) {
	t.Parallel()

	encoder := &Encoder{}
	decoder := &Decoder{}

	encoded, err := encoder.Encode(Message{
		Event:    42,
		BodySize: 1,
		Body:     []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	message, err := decoder.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if got, want := message.Event, uint32(42); got != want {
		t.Fatalf("Event = %d, want %d", got, want)
	}
	if got, want := message.BodySize, uint32(5); got != want {
		t.Fatalf("BodySize = %d, want %d", got, want)
	}
	if got, want := string(message.Body), "hello"; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
}

func TestValidateEncodedBodyLengthRejectsUint32Overflow(t *testing.T) {
	t.Parallel()

	maxAllocBody := uint64(math.MaxUint32)
	err := validateEncodedBodyLength(uint64(math.MaxUint32)+1, maxAllocBody)
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("validateEncodedBodyLength() error = %v, want ErrInvalidFrame", err)
	}
}

func TestValidateEncodedBodyLengthRejectsAllocationOverflow(t *testing.T) {
	t.Parallel()

	maxAllocBody := uint64(16)
	err := validateEncodedBodyLength(17, maxAllocBody)
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("validateEncodedBodyLength() error = %v, want ErrInvalidFrame", err)
	}
}
