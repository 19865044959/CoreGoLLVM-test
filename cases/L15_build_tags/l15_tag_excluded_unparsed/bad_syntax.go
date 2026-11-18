//go:build neverParsedSyntax

package main

// 本文件被 tag 排除：importer 不解析包体，下面的语法错误与重复符号都不可见。
import _ "no/such/package"

func baseImpl() string { return "syntax-file" }

func broken( {

func init() { baseInits += 100 }
