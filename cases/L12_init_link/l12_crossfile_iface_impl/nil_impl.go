package main

// nilTransport exercises typed-nil-interface semantics. The value stored in a
// Transport interface is a nil *nilTransport, so the interface itself is
// non-nil even though the dynamic pointer is nil.
type nilTransport struct{}

func (*nilTransport) Name() string { return "nil" }

func (*nilTransport) Send([]byte) int { panic("nil transport send") }

func (*nilTransport) Stats() Stats { return Stats{} }

func (*nilTransport) Close() error { return nil }
