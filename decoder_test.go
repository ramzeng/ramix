package ramix

import (
	"testing"
)

func TestDefaultDecoder_Decode(t *testing.T) {
	packed := []byte{1, 0, 0, 0, 2, 0, 0, 0, 97, 98}

	decoder := Decoder{}

	message, err := decoder.Decode(packed)

	if err != nil {
		t.Error(err)
	}

	if message.Event != 1 {
		t.Error("message.Event should be 1")
	}

	if message.BodySize != 2 {
		t.Error("message.BodySize should be 2")
	}
}
