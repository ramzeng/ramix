package ramix

import (
	"bytes"
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

func NewFrameDecoder(options ...FrameDecoderOption) *FrameDecoder {
	frameDecoderOptions := defaultFrameDecoderOptions

	for _, option := range options {
		option(&frameDecoderOptions)
	}

	frameDecoder := &FrameDecoder{
		FrameDecoderOptions: frameDecoderOptions,
	}

	frameDecoder.lengthFieldEndOffset = frameDecoder.LengthFieldOffset + frameDecoder.LengthFieldLength

	return frameDecoder
}

// FrameDecoder
// https://github.com/netty/netty/blob/4.1/codec/src/main/java/io/netty/handler/codec/LengthFieldBasedFrameDecoder.java
type FrameDecoder struct {
	FrameDecoderOptions

	lengthFieldEndOffset int

	failFast               bool
	discardingTooLongFrame bool
	tooLongFrameLength     int64
	bytesToDiscard         int64
	bytes                  []byte
	lock                   sync.Mutex
}

func (d *FrameDecoder) fail(frameLength int64) {
	if frameLength > 0 {
		panic(fmt.Sprintf("Adjusted frame length exceeds %d : %d - discarded", d.MaxFrameLength, frameLength))
	}

	panic(fmt.Sprintf("Adjusted frame length exceeds %d - discarded", d.MaxFrameLength))
}

func (d *FrameDecoder) discardingTooLongFrames(buffer *bytes.Buffer) {
	bytesToDiscard := d.bytesToDiscard

	localBytesToDiscard := math.Min(float64(bytesToDiscard), float64(buffer.Len()))

	buffer.Next(int(localBytesToDiscard))

	bytesToDiscard -= int64(localBytesToDiscard)

	d.bytesToDiscard = bytesToDiscard
	d.failIfNecessary(false)
}

func (d *FrameDecoder) getUnadjustedFrameLength(inputBuffer *bytes.Buffer, offset int, length int, order binary.ByteOrder) int64 {
	var frameLength int64

	bytesSlice := inputBuffer.Bytes()[offset : offset+length]
	buffer := bytes.NewBuffer(bytesSlice)

	switch length {
	case 1:
		var value uint8
		_ = binary.Read(buffer, order, &value)
		frameLength = int64(value)
	case 2:
		var value uint16
		_ = binary.Read(buffer, order, &value)
		frameLength = int64(value)
	case 3:
		if order == binary.LittleEndian {
			n := uint(bytesSlice[0]) | uint(bytesSlice[1])<<8 | uint(bytesSlice[2])<<16
			frameLength = int64(n)
		} else {
			n := uint(bytesSlice[2]) | uint(bytesSlice[1])<<8 | uint(bytesSlice[0])<<16
			frameLength = int64(n)
		}
	case 4:
		var value uint32
		_ = binary.Read(buffer, order, &value)
		frameLength = int64(value)
	case 8:
		_ = binary.Read(buffer, order, &frameLength)
	default:
		panic(fmt.Sprintf("unsupported LengthFieldLength: %d (expected: 1, 2, 3, 4, or 8)", d.LengthFieldLength))
	}

	return frameLength
}

func (d *FrameDecoder) failOnNegativeLengthField(inputBuffer *bytes.Buffer, frameLength int64, lengthFieldEndOffset int) {
	inputBuffer.Next(lengthFieldEndOffset)
	panic(fmt.Sprintf("negative pre-adjustment length field: %d", frameLength))
}

func (d *FrameDecoder) failIfNecessary(firstDetectionOfTooLongFrame bool) {
	if d.bytesToDiscard == 0 {
		tooLongFrameLength := d.tooLongFrameLength
		d.tooLongFrameLength = 0
		d.discardingTooLongFrame = false

		if !d.failFast || firstDetectionOfTooLongFrame {
			d.fail(tooLongFrameLength)
		}

		return
	}

	if d.failFast && firstDetectionOfTooLongFrame {
		d.fail(d.tooLongFrameLength)
	}
}

func (d *FrameDecoder) exceededFrameLength(inputBuffer *bytes.Buffer, frameLength int64) {
	discard := frameLength - int64(inputBuffer.Len())
	d.tooLongFrameLength = frameLength

	if discard < 0 {
		inputBuffer.Next(int(frameLength))
	} else {
		d.discardingTooLongFrame = true
		d.bytesToDiscard = discard
		inputBuffer.Next(inputBuffer.Len())
	}

	d.failIfNecessary(true)
}

func (d *FrameDecoder) failOnFrameLengthLessThanInitialBytesToStrip(inputBuffer *bytes.Buffer, frameLength int64, initialBytesToStrip int) {
	inputBuffer.Next(int(frameLength))
	panic(fmt.Sprintf("Adjusted frame length (%d) is less  than InitialBytesToStrip: %d", frameLength, initialBytesToStrip))
}

func (d *FrameDecoder) decode(inputBytes []byte) []byte {
	inputBuffer := bytes.NewBuffer(inputBytes)

	if d.discardingTooLongFrame {
		d.discardingTooLongFrames(inputBuffer)
	}

	if inputBuffer.Len() < d.lengthFieldEndOffset {
		return nil
	}

	frameLength := d.getUnadjustedFrameLength(inputBuffer, d.LengthFieldOffset, d.LengthFieldLength, d.ByteOrder)

	if frameLength < 0 {
		d.failOnNegativeLengthField(inputBuffer, frameLength, d.lengthFieldEndOffset)
	}

	frameLength += int64(d.LengthAdjustment) + int64(d.lengthFieldEndOffset)

	if uint64(frameLength) > d.MaxFrameLength {
		d.exceededFrameLength(inputBuffer, frameLength)
		return nil
	}

	frameLengthInt := int(frameLength)

	if inputBuffer.Len() < frameLengthInt {
		return nil
	}

	if d.InitialBytesToStrip > frameLengthInt {
		d.failOnFrameLengthLessThanInitialBytesToStrip(inputBuffer, frameLength, d.InitialBytesToStrip)
	}

	inputBuffer.Next(d.InitialBytesToStrip)

	outputBuffer := make([]byte, frameLengthInt-d.InitialBytesToStrip)

	_, _ = inputBuffer.Read(outputBuffer)

	return outputBuffer
}

func (d *FrameDecoder) Decode(bytes []byte) [][]byte {
	d.lock.Lock()
	defer d.lock.Unlock()

	d.bytes = append(d.bytes, bytes...)
	bytesSlices := make([][]byte, 0)

	for {
		bytesSlice := d.decode(d.bytes)

		if bytesSlice != nil {
			bytesSlices = append(bytesSlices, bytesSlice)

			frameLength := len(bytesSlice) + d.InitialBytesToStrip

			if frameLength > 0 {
				d.bytes = d.bytes[frameLength:]
			}

			continue
		}

		return bytesSlices
	}
}
