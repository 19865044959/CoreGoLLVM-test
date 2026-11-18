package main

// Stats completes tcpTransport's method set in a different file.
func (t *tcpTransport) Stats() Stats {
	return Stats{Sent: t.sent, Failed: t.failed}
}
