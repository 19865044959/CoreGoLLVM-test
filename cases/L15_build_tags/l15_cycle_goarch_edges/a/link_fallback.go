//go:build !amd64

package a

// 非 amd64：a 不导入 b，环的另一个方向在 b 中给出。
func Edge() string { return "a-standalone" }
