//go:build neverParsedImport

package main

// 被 tag 排除的文件不解析 import 图，也不参与重复定义检查。
import _ "no/such/package"

func baseImpl() string { return "import-file" }

var baseConst = 99

func init() { baseInits += 100 }
