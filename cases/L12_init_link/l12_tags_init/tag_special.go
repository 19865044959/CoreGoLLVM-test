//go:build l12special

package main

func init() { tagAdd("special.init") }

func specialOnly() string { return "special" }
