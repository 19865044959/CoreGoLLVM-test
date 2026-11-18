//go:build linux

package main

func init() { tagAdd("linux.init") }

func taggedImpl() string { return "linux" }
