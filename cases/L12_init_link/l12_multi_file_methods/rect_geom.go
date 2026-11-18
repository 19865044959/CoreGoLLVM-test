package main

func (r *rect) Area() int64 { return r.w * r.h }

func (r *rect) Perimeter() int64 { return 2 * (r.w + r.h) }
