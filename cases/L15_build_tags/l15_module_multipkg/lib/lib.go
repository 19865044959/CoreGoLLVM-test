package lib

import "l15_module_multipkg/lib/inner"

type Record struct {
	ID     int64
	Name   string
	Weight int64
}

var InitOK bool

func init() { InitOK = inner.Ready() }

func Build(id int64, name string) *Record {
	return &Record{ID: id, Name: name, Weight: inner.Scale(id) + inner.Bias}
}
