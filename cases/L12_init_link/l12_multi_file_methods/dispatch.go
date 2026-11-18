package main

// 接口派发辅助函数放在独立文件，调用方不感知具体类型所在文件。
func totalArea(shapes []Shape) int64 {
	var sum int64
	for _, s := range shapes {
		sum += s.Area()
	}
	return sum
}

func totalPerimeter(shapes []Shape) int64 {
	var sum int64
	for _, s := range shapes {
		sum += s.Perimeter()
	}
	return sum
}

func describe(shapes []Shape) string {
	out := ""
	for i, s := range shapes {
		if i > 0 {
			out += "|"
		}
		out += s.Name()
	}
	return out
}
