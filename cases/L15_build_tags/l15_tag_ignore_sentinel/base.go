package main

var liveMarker = "base"

var baseTokens []string

func marker() string { return liveMarker }

func init() { baseTokens = append(baseTokens, "base") }
