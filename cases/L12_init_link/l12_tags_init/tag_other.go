//go:build !linux

package main

func init() { tagAdd("other.init") }

func taggedImpl() string { return "other" }
