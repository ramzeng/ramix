package ramix

func newRequest(message Message) *Request {
	return &Request{
		Message: message,
	}
}

type Request struct {
	Message Message
}
