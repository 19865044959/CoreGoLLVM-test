//go:build !(linux && (!windows || amd64))

package main

func modernGroup() string { return "nogroup" }
