package main

// 文件名后缀 _amd64：仅 amd64 提供本入口。
const (
	entryName = "amd64"
	entryBias = int64(11)
)

func init() { entryInits = append(entryInits, "entry-amd64") }

func main() { runMain(entryName, entryBias) }
