package main

type udpTransport struct {
	name   string
	sent   int64
	failed int64
	closed bool
	data   *[32]byte
}

func newUDP(name string, key int) *udpTransport {
	data := new([32]byte)
	for i := range data {
		data[i] = byte(key + i)
	}
	return &udpTransport{name: name, data: data}
}

func (u *udpTransport) Name() string { return u.name }

func (u *udpTransport) Send(b []byte) int {
	if u.closed {
		u.failed++
		return -1
	}
	u.sent += int64(len(b))
	return len(b)
}

func (u *udpTransport) Stats() Stats {
	return Stats{Sent: u.sent, Failed: u.failed}
}

func (u *udpTransport) Close() error {
	if u.closed {
		return errClosed
	}
	u.closed = true
	return nil
}
