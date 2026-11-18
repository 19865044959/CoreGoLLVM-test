package iface

type Value interface {
	Get() int64
}

type Extra interface {
	Extra() string
}
