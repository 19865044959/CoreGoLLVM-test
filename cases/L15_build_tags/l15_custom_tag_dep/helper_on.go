//go:build tagFeat

package main

const featConst = "gated"

func init() { helperInit = true }

func featHelper() string { return "linked" }
