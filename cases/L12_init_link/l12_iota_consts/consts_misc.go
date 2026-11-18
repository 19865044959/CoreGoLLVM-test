package main

// untyped/typed 常量、字符串常量、位移与截断。
const Greeting = "go" + "1.24"

const Untyped62 = 1 << 62
const Int62 int64 = Untyped62

const U64Shift = uint64(1) << 63

const ExactInt = int(4.0)

const ArrLen = 6

const (
	ShiftA = 1 << (2 + iota)
	ShiftB
	ShiftC
)

const (
	untIntDiv   = 1 / 2
	untFloatDiv = 1.0 / 2
)

const smallPerm = perm(3)
