//go:build !amd64 && !arm64

package main

// 其余 GOARCH 的兜底入口；文件名无后缀关键字，靠 tag 与两个后缀文件互斥。
const (
	entryName = "generic"
	entryBias = int64(3)
)

func init() { entryInits = append(entryInits, "entry-generic") }

func main() { runMain(entryName, entryBias) }
