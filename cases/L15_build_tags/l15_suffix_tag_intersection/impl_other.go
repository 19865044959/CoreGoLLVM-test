//go:build !linux

package main

func impl() string { return "other" }

func init() { mark("other") }
