package ramix

type Message struct {
	Event    uint32
	Body     []byte
	BodySize uint32
}
