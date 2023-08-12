package main

import (
	"fmt"
	"github.com/ramzeng/ramix"
	"net"
)

func main() {
	socket, err := net.Dial("tcp4", "127.0.0.1:8899")

	if err != nil {
		fmt.Println("Dial error: ", err)
		return
	}

	encoder := ramix.Encoder{}
	decoder := ramix.Decoder{}

	go func() {
		for {
			buffer := make([]byte, 1024)

			_, err = socket.Read(buffer)

			if err != nil {
				fmt.Println("Read error: ", err)
				return
			}

			message, err := decoder.Decode(buffer, 1024)

			if err != nil {
				fmt.Println("Decode error: ", err)
				return
			}

			fmt.Printf("Server message: %s\n", message.Body)
		}
	}()

	for {
		var input string

		if _, err := fmt.Scanln(&input); err != nil {
			fmt.Println("input error: ", err)
			return
		}

		message := ramix.Message{
			Event: 0,
			Body:  []byte(input),
		}

		message.BodySize = uint32(len(message.Body))

		encodedMessage, err := encoder.Encode(message)

		if err != nil {
			fmt.Println("Encode error: ", err)
			return
		}

		_, err = socket.Write(encodedMessage)

		if err != nil {
			fmt.Println("Write error: ", err)
			return
		}
	}
}
