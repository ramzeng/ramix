package main

import (
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/ramzeng/ramix"
	"time"
)

func main() {
	socket, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:8900/ws", nil)

	if err != nil {
		fmt.Println("Dial error: ", err)
		return
	}

	encoder := ramix.Encoder{}
	decoder := ramix.Decoder{}

	for {
		message := ramix.Message{
			Event: 0,
			Body:  []byte("ping"),
		}

		message.BodySize = uint32(len(message.Body))

		encodedMessage, err := encoder.Encode(message)

		if err != nil {
			fmt.Println("Encode error: ", err)
			return
		}

		if err := socket.WriteMessage(websocket.BinaryMessage, encodedMessage); err != nil {
			fmt.Println("Write error: ", err)
			return
		}

		_, buffer, err := socket.ReadMessage()

		if err != nil {
			fmt.Println("Read error: ", err)
			return
		}

		message, err = decoder.Decode(buffer)

		if err != nil {
			fmt.Println("Decode error: ", err)
			return
		}

		fmt.Printf("Server message: %s\n", message.Body)

		time.Sleep(time.Second)
	}
}
