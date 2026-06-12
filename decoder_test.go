package ramix

import (
	"errors"
	"math"
	"testing"
)

func TestDecoderRejectsInvalidFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{name: "nil", data: nil},
		{name: "empty", data: []byte{}},
		{name: "short header 1", data: []byte{1}},
		{name: "short header 2", data: []byte{1, 0}},
		{name: "short header 3", data: []byte{1, 0, 0}},
		{name: "short header 4", data: []byte{1, 0, 0, 0}},
		{name: "short header 5", data: []byte{1, 0, 0, 0, 0}},
		{name: "short header 6", data: []byte{1, 0, 0, 0, 0, 0}},
		{name: "short header 7", data: []byte{1, 0, 0, 0, 0, 0, 0}},
		{name: "declared body longer than actual", data: []byte{1, 0, 0, 0, 2, 0, 0, 0, 'a'}},
		{name: "declared body shorter than actual", data: []byte{1, 0, 0, 0, 1, 0, 0, 0, 'a', 'b'}},
	}

	decoder := &Decoder{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decoder.Decode(tt.data)
			if !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("Decode() error = %v, want ErrInvalidFrame", err)
			}
		})
	}
}

func TestDecoderDecodesValidFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
		want Message
	}{
		{
			name: "empty body",
			data: []byte{7, 0, 0, 0, 0, 0, 0, 0},
			want: Message{Event: 7, BodySize: 0, Body: []byte{}},
		},
		{
			name: "non-empty body",
			data: []byte{1, 0, 0, 0, 2, 0, 0, 0, 'a', 'b'},
			want: Message{Event: 1, BodySize: 2, Body: []byte("ab")},
		},
	}

	decoder := &Decoder{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message, err := decoder.Decode(tt.data)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}

			if message.Event != tt.want.Event {
				t.Fatalf("Event = %d, want %d", message.Event, tt.want.Event)
			}
			if message.BodySize != tt.want.BodySize {
				t.Fatalf("BodySize = %d, want %d", message.BodySize, tt.want.BodySize)
			}
			if string(message.Body) != string(tt.want.Body) {
				t.Fatalf("Body = %q, want %q", message.Body, tt.want.Body)
			}
		})
	}
}

func TestDecoderRejectsActualBodySizeAboveUint32(t *testing.T) {
	t.Parallel()

	err := validateDecodedBodySize(uint32(math.MaxUint32), uint64(math.MaxUint32)+1)
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("validateDecodedBodySize() error = %v, want ErrInvalidFrame", err)
	}
}
