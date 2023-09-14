package ramix

import (
	"testing"
)

func TestFrameDecoder_Decode(t *testing.T) {
	bytes := []byte{
		0, 0, 0, 0, 4, 0, 0, 0, 112, 105, 110, 103,
		0, 0, 0, 0, 4, 0, 0, 0, 112, 105, 110, 103,
	}

	decoder := NewFrameDecoder(
		WithLengthFieldOffset(4),
		WithLengthFieldLength(4),
	)

	bytesSlices := decoder.Decode(bytes)

	if len(bytesSlices) != 2 {
		t.Error("frame decoder decode error")
	}
}
