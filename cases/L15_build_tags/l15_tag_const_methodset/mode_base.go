//go:build !tagExt

package main

const tagMode = "base"

func (t tagged) Base() string { return "base-method" }
