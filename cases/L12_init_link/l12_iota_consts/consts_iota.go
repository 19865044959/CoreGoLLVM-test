package main

// typed iota 枚举与位标志。
type weekday int

const (
	Sunday weekday = iota
	Monday
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
)

type perm uint8

const (
	PermRead perm = 1 << iota
	PermWrite
	PermExec
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
	GB
)

const (
	P, Q = iota, iota * 10
	R, S
)

const (
	first = iota + 1
	_
	third
)
