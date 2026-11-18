//go:build !tagAlt

package main

type baseSpeaker struct {
	msg *Message
}

var _ Speaker = (*baseSpeaker)(nil)

func newSpeaker(m *Message) Speaker { return &baseSpeaker{msg: m} }

func (s *baseSpeaker) Name() string { return "base" }

func (s *baseSpeaker) Say() string { return "base:" + s.msg.Text }
