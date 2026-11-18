//go:build ignore

package main

// ignore 是未设置的普通 tag：文件被排除，连 //go:embed 模式也不会被校验。
// 模式故意不匹配任何文件，包体也故意有语法错误与重复符号。
import _ "embed"

//go:embed no-such-file-*.bin
var embeddedPayload []byte

func baseImpl() string { return "ignore-embed" }

func broken( {

func init() { baseInits += 100 }
