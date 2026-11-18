//go:build tagFeat

package main

func featImpl() string { return "feat+" + featHelper() + ":" + featConst }
