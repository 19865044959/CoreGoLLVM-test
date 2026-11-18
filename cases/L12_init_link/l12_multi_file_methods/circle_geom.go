package main

func (c circle) Area() int64 { return 3 * c.r * c.r }

func (c circle) Perimeter() int64 { return 6 * c.r }
