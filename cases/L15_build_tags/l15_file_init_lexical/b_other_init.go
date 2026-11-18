//go:build !linux

package main

func init() {
	seq = append(seq, "b-other")
	payload = &initNode{v: 21, name: "tagged-init"}
}
