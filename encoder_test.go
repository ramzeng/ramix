package ramix

import (
	"testing"
)

func TestDefaultEncoder_Encode(t *testing.T) {
	message := Message{
		Event:    1,
		BodySize: 2,
		Body:     []byte("ab"),
	}

	encoder := Encoder{}

	packedMessage, err := encoder.Encode(message)

	if err != nil {
		t.Error(err)
	}

	if len(packedMessage) != 10 {
		t.Error("packedMessage length should be 10")
	}

	if packedMessage[0] != 1 {
		t.Error("packedMessage[0] should be 1")
	}

	if packedMessage[4] != 2 {
		t.Error("packedMessage[8] should be 2")
	}

	if packedMessage[8] != 97 {
		t.Error("packedMessage[12] should be 97")
	}

	if packedMessage[9] != 98 {
		t.Error("packedMessage[13] should be 98")
	}
}
