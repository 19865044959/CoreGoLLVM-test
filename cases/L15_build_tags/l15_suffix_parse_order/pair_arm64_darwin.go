package main

// 末段是 GOOS=darwin，arm64 不是 GOOS，因此按 _darwin 后缀匹配（任意 GOARCH）。
func init() { mark("pair_arm64_darwin") }
