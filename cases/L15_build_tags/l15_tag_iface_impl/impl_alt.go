//go:build tagAlt

package main

type altSpeaker struct {
	msg *Message
}

var _ Speaker = (*altSpeaker)(nil)

func newSpeaker(m *Message) Speaker { return &altSpeaker{msg: m} }

func (s *altSpeaker) Name() string { return "alt" }

func (s *altSpeaker) Say() string { return "alt:" + s.msg.Text }
