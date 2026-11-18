//go:build !((tagA || tagB) && !tagC)

package main

func exprImpl() string { return "off" }
