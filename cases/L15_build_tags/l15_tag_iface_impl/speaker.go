package main

type Speaker interface {
	Name() string
	Say() string
}

type Message struct {
	ID   int64
	Text string
	Next *Message
}
