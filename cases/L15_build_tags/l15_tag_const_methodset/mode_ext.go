//go:build tagExt

package main

const tagMode = "ext"

func (t tagged) Base() string { return "ext-base" }

func (t tagged) Extra() string { return "ext-extra" }
