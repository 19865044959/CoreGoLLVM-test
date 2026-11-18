//go:build (linux || freebsd) && 386

package main

func osArchImpl() string { return "unix-386" }
