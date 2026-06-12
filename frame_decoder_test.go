package ramix

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestNewFrameDecoderRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options []FrameDecoderOption
	}{
		{
			name: "unsupported length field length",
			options: []FrameDecoderOption{
				WithLengthFieldLength(5),
				WithMaxFrameLength(64),
			},
		},
		{
			name: "negative length field offset",
			options: []FrameDecoderOption{
				WithLengthFieldOffset(-1),
				WithLengthFieldLength(4),
				WithMaxFrameLength(64),
			},
		},
		{
			name: "negative initial bytes to strip",
			options: []FrameDecoderOption{
				WithLengthFieldLength(4),
				WithInitialBytesToStrip(-1),
				WithMaxFrameLength(64),
			},
		},
		{
			name: "zero max frame length",
			options: []FrameDecoderOption{
				WithLengthFieldLength(4),
				WithMaxFrameLength(0),
			},
		},
		{
			name: "length field end beyond max frame length",
			options: []FrameDecoderOption{
				WithLengthFieldOffset(4),
				WithLengthFieldLength(4),
				WithMaxFrameLength(7),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoder, err := NewFrameDecoder(tt.options...)
			if decoder != nil {
				t.Fatalf("NewFrameDecoder() decoder = %#v, want nil", decoder)
			}
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("NewFrameDecoder() error = %v, want ErrInvalidConfiguration", err)
			}
		})
	}
}

func TestNewFrameDecoderRejectsNilOption(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(nil)
	if decoder != nil {
		t.Fatalf("NewFrameDecoder() decoder = %#v, want nil", decoder)
	}
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewFrameDecoder() error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestNewFrameDecoderRejectsMinimumIntLengthAdjustment(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldLength(4),
		WithLengthAdjustment(math.MinInt),
		WithMaxFrameLength(64),
	)
	if decoder != nil {
		t.Fatalf("NewFrameDecoder() decoder = %#v, want nil", decoder)
	}
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewFrameDecoder() error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestNewFrameDecoderRejectsImpossibleNegativeLengthAdjustment(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(1),
		WithLengthAdjustment(-256),
		WithMaxFrameLength(512),
	)
	if decoder != nil {
		t.Fatalf("NewFrameDecoder() decoder = %#v, want nil", decoder)
	}
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewFrameDecoder() error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestNewFrameDecoderAcceptsBoundaryNegativeLengthAdjustment(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(1),
		WithLengthAdjustment(-255),
		WithMaxFrameLength(512),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	frames, err := decoder.Decode([]byte{255})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], []byte{255}) {
		t.Fatalf("frame = %v, want %v", frames[0], []byte{255})
	}
}

func TestNewFrameDecoderRejectsImpossiblePositiveLengthAdjustment(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(1),
		WithLengthAdjustment(8),
		WithMaxFrameLength(8),
	)
	if decoder != nil {
		t.Fatalf("NewFrameDecoder() decoder = %#v, want nil", decoder)
	}
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewFrameDecoder() error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestNewFrameDecoderAcceptsBoundaryPositiveLengthAdjustment(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(1),
		WithLengthAdjustment(7),
		WithMaxFrameLength(8),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	frames, err := decoder.Decode([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], []byte{0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("frame = %v, want %v", frames[0], []byte{0, 0, 0, 0, 0, 0, 0, 0})
	}
}

func TestNewFrameDecoderRejectsPositiveLengthAdjustmentThatExceedsPlatformInt(t *testing.T) {
	t.Parallel()

	maxInt := int(^uint(0) >> 1)

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(1),
		WithLengthAdjustment(maxInt),
		WithMaxFrameLength(math.MaxUint64),
	)
	if decoder != nil {
		t.Fatalf("NewFrameDecoder() decoder = %#v, want nil", decoder)
	}
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("NewFrameDecoder() error = %v, want ErrInvalidConfiguration", err)
	}
}

func TestNewFrameDecoderAcceptsPositiveLengthAdjustmentAtPlatformIntBoundary(t *testing.T) {
	t.Parallel()

	maxInt := int(^uint(0) >> 1)

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(1),
		WithLengthAdjustment(maxInt-1),
		WithMaxFrameLength(math.MaxUint64),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}
	if decoder == nil {
		t.Fatal("NewFrameDecoder() decoder = nil, want non-nil")
	}
}

func TestNewFrameDecoderPreservesValidOptions(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithByteOrder(binary.BigEndian),
		WithMaxFrameLength(64),
		WithLengthFieldOffset(1),
		WithLengthFieldLength(2),
		WithLengthAdjustment(3),
		WithInitialBytesToStrip(1),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	if decoder.ByteOrder != binary.BigEndian {
		t.Fatalf("ByteOrder = %T, want BigEndian", decoder.ByteOrder)
	}
	if got, want := decoder.MaxFrameLength, uint64(64); got != want {
		t.Fatalf("MaxFrameLength = %d, want %d", got, want)
	}
	if got, want := decoder.LengthFieldOffset, 1; got != want {
		t.Fatalf("LengthFieldOffset = %d, want %d", got, want)
	}
	if got, want := decoder.LengthFieldLength, 2; got != want {
		t.Fatalf("LengthFieldLength = %d, want %d", got, want)
	}
	if got, want := decoder.LengthAdjustment, 3; got != want {
		t.Fatalf("LengthAdjustment = %d, want %d", got, want)
	}
	if got, want := decoder.InitialBytesToStrip, 1; got != want {
		t.Fatalf("InitialBytesToStrip = %d, want %d", got, want)
	}
}

func TestNewFrameDecoderAcceptsNegativeLengthAdjustmentWhenFrameIncludesHeader(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(4),
		WithLengthAdjustment(-4),
		WithMaxFrameLength(64),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	frame := []byte{8, 0, 0, 0, 'd', 'a', 't', 'a'}
	frames, err := decoder.Decode(frame)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], frame) {
		t.Fatalf("frame = %v, want %v", frames[0], frame)
	}
}

func TestFrameDecoderRejectsTooSmallNegativeAdjustedLengthAndClearsState(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(0),
		WithLengthFieldLength(4),
		WithLengthAdjustment(-4),
		WithMaxFrameLength(64),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	_, err = decoder.Decode([]byte{2, 0, 0, 0})
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("Decode() error = %v, want ErrInvalidFrame", err)
	}

	valid := []byte{8, 0, 0, 0, 'o', 'k', '!', '!'}
	frames, err := decoder.Decode(valid)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], valid) {
		t.Fatalf("frame = %v, want %v", frames[0], valid)
	}
}

func TestFrameDecoderSplitFrameReturnsNothingUntilComplete(t *testing.T) {
	t.Parallel()

	decoder := mustNewRamixFrameDecoder(t, 64)
	frame := mustEncodeMessageFrame(t, 1, []byte("one"))

	frames, err := decoder.Decode(frame[:5])
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("len(frames) = %d, want 0", len(frames))
	}

	frames, err = decoder.Decode(frame[5:])
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	assertDecodedMessage(t, frames[0], 1, "one")
}

func TestFrameDecoderReturnsCoalescedFramesInOrder(t *testing.T) {
	t.Parallel()

	decoder := mustNewRamixFrameDecoder(t, 64)
	first := mustEncodeMessageFrame(t, 1, []byte("one"))
	second := mustEncodeMessageFrame(t, 2, []byte("two"))

	frames, err := decoder.Decode(append(first, second...))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len(frames) = %d, want 2", len(frames))
	}

	assertDecodedMessage(t, frames[0], 1, "one")
	assertDecodedMessage(t, frames[1], 2, "two")
}

func TestFrameDecoderSplitsThenCoalescesBoundaryFramesInOrder(t *testing.T) {
	t.Parallel()

	decoder := mustNewRamixFrameDecoder(t, 64)
	first := mustEncodeMessageFrame(t, 1, []byte("one"))
	second := mustEncodeMessageFrame(t, 2, []byte("two"))

	frames, err := decoder.Decode(first[:5])
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("len(frames) = %d, want 0", len(frames))
	}

	frames, err = decoder.Decode(append(first[5:], second...))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("len(frames) = %d, want 2", len(frames))
	}

	assertDecodedMessage(t, frames[0], 1, "one")
	assertDecodedMessage(t, frames[1], 2, "two")
}

func TestFrameDecoderSupportsNonZeroOffsetAndAdjustmentWithByteOrder(t *testing.T) {
	t.Parallel()

	// This frame layout matches:
	//   prefix(2) + length field(2) + payload(8)
	// The length field stores the total frame length (12), and the decoder uses
	// LengthAdjustment(-4) so the adjusted frame length becomes 12 again:
	//   total frame length = field value + lengthFieldEndOffset(4) - 4
	// With InitialBytesToStrip(4), Decode returns only the payload bytes.
	decoder, err := NewFrameDecoder(
		WithByteOrder(binary.BigEndian),
		WithLengthFieldOffset(2),
		WithLengthFieldLength(2),
		WithLengthAdjustment(-4),
		WithInitialBytesToStrip(4),
		WithMaxFrameLength(64),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	payload := []byte("payload!")
	frame := make([]byte, 4+len(payload))
	copy(frame[:2], []byte("PR"))
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(frame)))
	copy(frame[4:], payload)

	frames, err := decoder.Decode(frame)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if !bytes.Equal(frames[0], payload) {
		t.Fatalf("frame = %q, want %q", frames[0], payload)
	}
}

func TestFrameDecoderSupportsConfiguredLengthFieldWidthsAndByteOrders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		order     binary.ByteOrder
		fieldSize int
	}{
		{name: "1 byte little endian", order: binary.LittleEndian, fieldSize: 1},
		{name: "1 byte big endian", order: binary.BigEndian, fieldSize: 1},
		{name: "2 byte little endian", order: binary.LittleEndian, fieldSize: 2},
		{name: "2 byte big endian", order: binary.BigEndian, fieldSize: 2},
		{name: "3 byte little endian", order: binary.LittleEndian, fieldSize: 3},
		{name: "3 byte big endian", order: binary.BigEndian, fieldSize: 3},
		{name: "4 byte little endian", order: binary.LittleEndian, fieldSize: 4},
		{name: "4 byte big endian", order: binary.BigEndian, fieldSize: 4},
		{name: "8 byte little endian", order: binary.LittleEndian, fieldSize: 8},
		{name: "8 byte big endian", order: binary.BigEndian, fieldSize: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoder, err := NewFrameDecoder(
				WithByteOrder(tt.order),
				WithLengthFieldLength(tt.fieldSize),
				WithInitialBytesToStrip(tt.fieldSize),
				WithMaxFrameLength(64),
			)
			if err != nil {
				t.Fatalf("NewFrameDecoder() error = %v", err)
			}

			frame := encodeLengthPrefixedFrame(tt.order, tt.fieldSize, []byte("payload"))

			frames, err := decoder.Decode(frame)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if len(frames) != 1 {
				t.Fatalf("len(frames) = %d, want 1", len(frames))
			}
			if got, want := string(frames[0]), "payload"; got != want {
				t.Fatalf("frame = %q, want %q", got, want)
			}
		})
	}
}

func TestFrameDecoderRejectsInvalidAdjustedOrOverflowingLengths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		decoder func(t *testing.T) *FrameDecoder
		data    []byte
	}{
		{
			name: "adjusted length exceeds platform int range",
			decoder: func(t *testing.T) *FrameDecoder {
				decoder, err := NewFrameDecoder(
					WithLengthFieldLength(8),
					WithLengthAdjustment(1),
					WithMaxFrameLength(math.MaxUint64),
				)
				if err != nil {
					t.Fatalf("NewFrameDecoder() error = %v", err)
				}
				return decoder
			},
			data: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		},
		{
			name: "overflowing 8-byte length",
			decoder: func(t *testing.T) *FrameDecoder {
				decoder, err := NewFrameDecoder(
					WithLengthFieldLength(8),
					WithMaxFrameLength(math.MaxUint64),
				)
				if err != nil {
					t.Fatalf("NewFrameDecoder() error = %v", err)
				}
				return decoder
			},
			data: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.decoder(t).Decode(tt.data)
			if !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("Decode() error = %v, want ErrInvalidFrame", err)
			}
		})
	}
}

func TestFrameDecoderRejectsFramesExceedingMaxFrameLength(t *testing.T) {
	t.Parallel()

	decoder := mustNewRamixFrameDecoder(t, 10)
	frame := mustEncodeMessageFrame(t, 1, []byte("abc"))

	_, err := decoder.Decode(frame)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Decode() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameDecoderRejectsInitialBytesToStripPastFrameLength(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldLength(1),
		WithInitialBytesToStrip(2),
		WithMaxFrameLength(64),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	_, err = decoder.Decode([]byte{0})
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("Decode() error = %v, want ErrInvalidFrame", err)
	}
}

func TestFrameDecoderClearsBufferedStateAfterInvalidFrame(t *testing.T) {
	t.Parallel()

	decoder, err := NewFrameDecoder(
		WithLengthFieldLength(8),
		WithInitialBytesToStrip(8),
		WithMaxFrameLength(math.MaxUint64),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	_, err = decoder.Decode([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("Decode() error = %v, want ErrInvalidFrame", err)
	}

	valid := encodeLengthPrefixedFrame(binary.LittleEndian, 8, []byte("ok"))
	frames, err := decoder.Decode(valid)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	if got, want := string(frames[0]), "ok"; got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

func TestFrameDecoderClearsBufferedStateAfterOversizedFrame(t *testing.T) {
	t.Parallel()

	decoder := mustNewRamixFrameDecoder(t, 9)

	oversized := mustEncodeMessageFrame(t, 1, []byte("ab"))
	_, err := decoder.Decode(oversized)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Decode() error = %v, want ErrFrameTooLarge", err)
	}

	valid := mustEncodeMessageFrame(t, 2, []byte("z"))
	frames, err := decoder.Decode(valid)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}
	assertDecodedMessage(t, frames[0], 2, "z")
}

func TestFrameDecoderReturnsOwnedFrameBytes(t *testing.T) {
	t.Parallel()

	decoder := mustNewRamixFrameDecoder(t, 64)
	frame := mustEncodeMessageFrame(t, 3, []byte("abc"))
	original := append([]byte(nil), frame...)

	frames, err := decoder.Decode(frame)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("len(frames) = %d, want 1", len(frames))
	}

	for i := range frame {
		frame[i] = 0
	}

	if !bytes.Equal(frames[0], original) {
		t.Fatalf("returned frame mutated with input: got %v, want %v", frames[0], original)
	}
	assertDecodedMessage(t, frames[0], 3, "abc")
}

func mustNewRamixFrameDecoder(t *testing.T, maxFrameLength uint64) *FrameDecoder {
	t.Helper()

	decoder, err := NewFrameDecoder(
		WithLengthFieldOffset(4),
		WithLengthFieldLength(4),
		WithMaxFrameLength(maxFrameLength),
	)
	if err != nil {
		t.Fatalf("NewFrameDecoder() error = %v", err)
	}

	return decoder
}

func mustEncodeMessageFrame(t *testing.T, event uint32, body []byte) []byte {
	t.Helper()

	frame, err := (&Encoder{}).Encode(Message{
		Event: event,
		Body:  body,
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	return frame
}

func assertDecodedMessage(t *testing.T, frame []byte, wantEvent uint32, wantBody string) {
	t.Helper()

	message, err := (&Decoder{}).Decode(frame)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got := message.Event; got != wantEvent {
		t.Fatalf("Event = %d, want %d", got, wantEvent)
	}
	if got, want := string(message.Body), wantBody; got != want {
		t.Fatalf("Body = %q, want %q", got, want)
	}
}

func encodeLengthPrefixedFrame(order binary.ByteOrder, fieldSize int, payload []byte) []byte {
	frame := make([]byte, fieldSize+len(payload))
	putLength(frame[:fieldSize], order, uint64(len(payload)))
	copy(frame[fieldSize:], payload)
	return frame
}

func putLength(dst []byte, order binary.ByteOrder, value uint64) {
	switch len(dst) {
	case 1:
		dst[0] = byte(value)
	case 2:
		order.PutUint16(dst, uint16(value))
	case 3:
		if order == binary.LittleEndian {
			dst[0] = byte(value)
			dst[1] = byte(value >> 8)
			dst[2] = byte(value >> 16)
		} else {
			dst[0] = byte(value >> 16)
			dst[1] = byte(value >> 8)
			dst[2] = byte(value)
		}
	case 4:
		order.PutUint32(dst, uint32(value))
	case 8:
		order.PutUint64(dst, value)
	default:
		panic("unsupported test length field size")
	}
}
