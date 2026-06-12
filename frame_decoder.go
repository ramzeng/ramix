package ramix

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

var defaultFrameDecoderOptions = FrameDecoderOptions{
	ByteOrder:           binary.LittleEndian,
	MaxFrameLength:      1 << 20,
	LengthFieldOffset:   0,
	LengthFieldLength:   0,
	LengthAdjustment:    0,
	InitialBytesToStrip: 0,
}

type FrameDecoderOptions struct {
	ByteOrder           binary.ByteOrder
	MaxFrameLength      uint64
	LengthFieldOffset   int
	LengthFieldLength   int
	LengthAdjustment    int
	InitialBytesToStrip int
}

type FrameDecoderOption func(*FrameDecoderOptions)

func WithByteOrder(byteOrder binary.ByteOrder) FrameDecoderOption {
	return func(o *FrameDecoderOptions) {
		o.ByteOrder = byteOrder
	}
}

func WithMaxFrameLength(maxFrameLength uint64) FrameDecoderOption {
	return func(o *FrameDecoderOptions) {
		o.MaxFrameLength = maxFrameLength
	}
}

func WithLengthFieldOffset(lengthFieldOffset int) FrameDecoderOption {
	return func(o *FrameDecoderOptions) {
		o.LengthFieldOffset = lengthFieldOffset
	}
}

func WithLengthFieldLength(lengthFieldLength int) FrameDecoderOption {
	return func(o *FrameDecoderOptions) {
		o.LengthFieldLength = lengthFieldLength
	}
}

func WithLengthAdjustment(lengthAdjustment int) FrameDecoderOption {
	return func(o *FrameDecoderOptions) {
		o.LengthAdjustment = lengthAdjustment
	}
}

func WithInitialBytesToStrip(initialBytesToStrip int) FrameDecoderOption {
	return func(o *FrameDecoderOptions) {
		o.InitialBytesToStrip = initialBytesToStrip
	}
}

func NewFrameDecoder(options ...FrameDecoderOption) (*FrameDecoder, error) {
	frameDecoderOptions := defaultFrameDecoderOptions

	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: frame decoder option must not be nil", ErrInvalidConfiguration)
		}
		option(&frameDecoderOptions)
	}

	frameDecoder := &FrameDecoder{
		FrameDecoderOptions: frameDecoderOptions,
	}
	frameDecoder.lengthFieldEndOffset = frameDecoder.LengthFieldOffset + frameDecoder.LengthFieldLength

	if err := frameDecoder.validateConfiguration(); err != nil {
		return nil, err
	}

	return frameDecoder, nil
}

// FrameDecoder
// https://github.com/netty/netty/blob/4.1/codec/src/main/java/io/netty/handler/codec/LengthFieldBasedFrameDecoder.java
type FrameDecoder struct {
	FrameDecoderOptions

	lengthFieldEndOffset int
	bytes                []byte
	lock                 sync.Mutex
}

func (d *FrameDecoder) validateConfiguration() error {
	if d.ByteOrder == nil {
		return fmt.Errorf("%w: byte order must not be nil", ErrInvalidConfiguration)
	}

	switch d.LengthFieldLength {
	case 1, 2, 3, 4, 8:
	default:
		return fmt.Errorf("%w: unsupported length field length %d", ErrInvalidConfiguration, d.LengthFieldLength)
	}

	if d.LengthFieldOffset < 0 {
		return fmt.Errorf("%w: length field offset must be non-negative: %d", ErrInvalidConfiguration, d.LengthFieldOffset)
	}

	if d.InitialBytesToStrip < 0 {
		return fmt.Errorf("%w: initial bytes to strip must be non-negative: %d", ErrInvalidConfiguration, d.InitialBytesToStrip)
	}

	if d.MaxFrameLength == 0 {
		return fmt.Errorf("%w: max frame length must be positive", ErrInvalidConfiguration)
	}

	if d.lengthFieldEndOffset < 0 {
		return fmt.Errorf("%w: length field end offset overflowed", ErrInvalidConfiguration)
	}

	platformMaxInt := uint64(int(^uint(0) >> 1))
	effectiveMax := d.MaxFrameLength
	if effectiveMax > platformMaxInt {
		effectiveMax = platformMaxInt
	}

	if uint64(d.lengthFieldEndOffset) > effectiveMax {
		return fmt.Errorf("%w: length field end offset %d exceeds feasible maximum %d", ErrInvalidConfiguration, d.lengthFieldEndOffset, effectiveMax)
	}

	if d.LengthAdjustment == math.MinInt {
		return fmt.Errorf("%w: length adjustment %d cannot be negated safely", ErrInvalidConfiguration, d.LengthAdjustment)
	}

	maxRepresentableUnadjusted, err := maxRepresentableUnadjustedLength(d.LengthFieldLength)
	if err != nil {
		return err
	}

	if d.LengthAdjustment < 0 {
		magnitude := uint64(-int64(d.LengthAdjustment))
		if magnitude > maxRepresentableUnadjusted {
			return fmt.Errorf("%w: negative length adjustment magnitude %d exceeds max representable unadjusted length %d", ErrInvalidConfiguration, magnitude, maxRepresentableUnadjusted)
		}

		return nil
	}

	maxPositiveAdjustment := effectiveMax - uint64(d.lengthFieldEndOffset)
	if uint64(d.LengthAdjustment) > maxPositiveAdjustment {
		return fmt.Errorf("%w: positive length adjustment %d exceeds feasible maximum %d", ErrInvalidConfiguration, d.LengthAdjustment, maxPositiveAdjustment)
	}

	return nil
}

func (d *FrameDecoder) Decode(input []byte) ([][]byte, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.bytes = append(d.bytes, input...)
	frames := make([][]byte, 0)

	for {
		frame, consumed, complete, err := d.decodeOne(d.bytes)
		if err != nil {
			d.bytes = nil
			return nil, err
		}

		if !complete {
			return frames, nil
		}

		frames = append(frames, frame)
		d.bytes = d.bytes[consumed:]
	}
}

func (d *FrameDecoder) decodeOne(input []byte) ([]byte, int, bool, error) {
	if len(input) < d.lengthFieldEndOffset {
		return nil, 0, false, nil
	}

	unadjustedFrameLength, err := d.readLengthField(input)
	if err != nil {
		return nil, 0, false, err
	}

	frameLength, ok := d.adjustFrameLength(unadjustedFrameLength)
	if !ok {
		return nil, 0, false, fmt.Errorf("%w: adjusted frame length overflow: unadjusted=%d adjustment=%d header=%d", ErrInvalidFrame, unadjustedFrameLength, d.LengthAdjustment, d.lengthFieldEndOffset)
	}

	if frameLength < uint64(d.lengthFieldEndOffset) {
		return nil, 0, false, fmt.Errorf("%w: adjusted frame length %d is smaller than header length %d", ErrInvalidFrame, frameLength, d.lengthFieldEndOffset)
	}

	if frameLength > d.MaxFrameLength {
		return nil, 0, false, fmt.Errorf("%w: adjusted frame length %d exceeds max frame length %d", ErrFrameTooLarge, frameLength, d.MaxFrameLength)
	}

	maxInt := uint64(int(^uint(0) >> 1))
	if frameLength > maxInt {
		return nil, 0, false, fmt.Errorf("%w: adjusted frame length %d exceeds platform int max %d", ErrInvalidFrame, frameLength, maxInt)
	}

	frameLengthInt := int(frameLength)
	if len(input) < frameLengthInt {
		return nil, 0, false, nil
	}

	if d.InitialBytesToStrip > frameLengthInt {
		return nil, 0, false, fmt.Errorf("%w: initial bytes to strip %d exceeds frame length %d", ErrInvalidFrame, d.InitialBytesToStrip, frameLengthInt)
	}

	frame := append([]byte(nil), input[d.InitialBytesToStrip:frameLengthInt]...)

	return frame, frameLengthInt, true, nil
}

func (d *FrameDecoder) readLengthField(input []byte) (uint64, error) {
	field := input[d.LengthFieldOffset:d.lengthFieldEndOffset]

	switch d.LengthFieldLength {
	case 1:
		return uint64(field[0]), nil
	case 2:
		return uint64(d.ByteOrder.Uint16(field)), nil
	case 3:
		if d.ByteOrder == binary.LittleEndian {
			return uint64(field[0]) | uint64(field[1])<<8 | uint64(field[2])<<16, nil
		}
		return uint64(field[2]) | uint64(field[1])<<8 | uint64(field[0])<<16, nil
	case 4:
		return uint64(d.ByteOrder.Uint32(field)), nil
	case 8:
		value := d.ByteOrder.Uint64(field)
		if value > math.MaxInt64 {
			return 0, fmt.Errorf("%w: 8-byte length field %d exceeds max signed frame length %d", ErrInvalidFrame, value, uint64(math.MaxInt64))
		}
		return value, nil
	default:
		return 0, fmt.Errorf("%w: unsupported length field length %d", ErrInvalidConfiguration, d.LengthFieldLength)
	}
}

func (d *FrameDecoder) adjustFrameLength(unadjusted uint64) (uint64, bool) {
	if unadjusted > math.MaxUint64-uint64(d.lengthFieldEndOffset) {
		return 0, false
	}

	adjusted := unadjusted + uint64(d.lengthFieldEndOffset)

	adjustment := int64(d.LengthAdjustment)
	if adjustment >= 0 {
		positive := uint64(adjustment)
		if adjusted > math.MaxUint64-positive {
			return 0, false
		}
		return adjusted + positive, true
	}

	if adjustment == math.MinInt64 {
		return 0, false
	}

	negative := uint64(-adjustment)
	if adjusted < negative {
		return 0, false
	}

	return adjusted - negative, true
}

func maxRepresentableUnadjustedLength(lengthFieldLength int) (uint64, error) {
	switch lengthFieldLength {
	case 1:
		return math.MaxUint8, nil
	case 2:
		return math.MaxUint16, nil
	case 3:
		return 1<<24 - 1, nil
	case 4:
		return math.MaxUint32, nil
	case 8:
		return math.MaxInt64, nil
	default:
		return 0, fmt.Errorf("%w: unsupported length field length %d", ErrInvalidConfiguration, lengthFieldLength)
	}
}
