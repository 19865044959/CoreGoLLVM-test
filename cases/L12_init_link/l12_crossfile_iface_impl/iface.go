package main

import "errors"

var errClosed = errors.New("transport already closed")

type Stats struct {
	Sent   int64
	Failed int64
}

type Transport interface {
	Name() string
	Send([]byte) int
	Stats() Stats
}

type Closer interface {
	Close() error
}

// TransportCloser embeds two interfaces; implementations must provide the
// union of their method sets.
type TransportCloser interface {
	Transport
	Closer
}

type named interface {
	Name() string
}

// Compile-time implementation checks. The concrete types live in other files.
var (
	_ Transport       = (*tcpTransport)(nil)
	_ TransportCloser = (*tcpTransport)(nil)
	_ named           = (*tcpTransport)(nil)
	_ Transport       = (*udpTransport)(nil)
	_ TransportCloser = (*udpTransport)(nil)
	_ Transport       = (*nilTransport)(nil)
	_ Closer          = (*nilTransport)(nil)
)
