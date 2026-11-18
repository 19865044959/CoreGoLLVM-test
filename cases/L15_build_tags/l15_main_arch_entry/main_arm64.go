package main

// 文件名后缀 _arm64：仅 arm64 提供本入口。
const (
	entryName = "arm64"
	entryBias = int64(7)
)

func init() { entryInits = append(entryInits, "entry-arm64") }

func main() { runMain(entryName, entryBias) }
