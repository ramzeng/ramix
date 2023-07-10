package ramix

func newRequest(message Message, data []byte) *Request {
	return &Request{
		Message: message,
		Data:    data,
		RawData: data,
	}
}

type Request struct {
	Message Message
	Data    []byte
	RawData []byte
}
