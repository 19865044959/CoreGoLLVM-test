package main

type Shape interface {
	Area() int64
	Perimeter() int64
	Name() string
}

type base struct {
	tag string
}

func (b base) Tag() string { return b.tag }

// 编译期方法集断言：circle 值/指针都实现，rect 仅指针实现。
var (
	_ Shape = circle{}
	_ Shape = (*circle)(nil)
	_ Shape = (*rect)(nil)
	_ Shape = namedCircle{}
)
