//go:build linux

package main

func init() {
	seq = append(seq, "b-linux")
	payload = &initNode{v: 21, name: "tagged-init"}
}
