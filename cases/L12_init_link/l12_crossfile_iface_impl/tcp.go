package main

type tcpTransport struct {
	name   string
	sent   int64
	failed int64
	closed bool
	data   *[32]byte
}

func newTCP(name string, key int) *tcpTransport {
	data := new([32]byte)
	for i := range data {
		data[i] = byte(key + i)
	}
	return &tcpTransport{name: name, data: data}
}

func (t *tcpTransport) Name() string { return t.name }

func (t *tcpTransport) Send(b []byte) int {
	if t.closed {
		t.failed++
		return -1
	}
	t.sent += int64(len(b))
	return len(b)
}

func (t *tcpTransport) Close() error {
	if t.closed {
		return errClosed
	}
	t.closed = true
	return nil
}
