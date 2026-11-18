package main

import (
	"fmt"
	"os"
)

type cfg struct {
	Tag   string
	Value int
}

var (
	// childMode is read by a package-level variable initializer, i.e. before
	// any init function runs.
	childMode = os.Getenv("L12_VARPANIC_CHILD")

	initTrace []string

	beforeVar = registerVar("before")
	loaded    = loadConfig(childMode)
	afterVar  = registerVar("after")
	recovered = safeLoad(childMode)
)

func registerVar(s string) string {
	initTrace = append(initTrace, "var:"+s)
	return s
}

// loadConfig panics during package variable initialization in "panic" mode.
// There is no recover boundary here, so the process must terminate before any
// init function or main runs.
func loadConfig(mode string) *cfg {
	if mode == "panic" {
		panic("l12 var init boom")
	}
	return &cfg{Tag: "loaded", Value: 41}
}

// safeLoad has its own recover boundary inside a function called by a package
// variable initializer. In "recover" mode it converts the panic into a
// fallback value and variable initialization continues.
func safeLoad(mode string) (out *cfg) {
	defer func() {
		if r := recover(); r != nil {
			out = &cfg{Tag: "fallback", Value: 7}
			initTrace = append(initTrace, "var:recover")
		}
	}()
	if mode == "recover" {
		panic("l12 safe load boom")
	}
	return &cfg{Tag: "safe", Value: 43}
}

func init() {
	initTrace = append(initTrace, "init")
	if childMode != "" {
		fmt.Println("CHILD-INIT-RAN")
	}
}
