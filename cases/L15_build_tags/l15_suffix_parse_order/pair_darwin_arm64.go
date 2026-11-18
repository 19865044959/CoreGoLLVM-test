package main

// 末两段是合法的 GOOS_GOARCH 组合（darwin + arm64），必须两者同时匹配。
func init() { mark("pair_darwin_arm64") }
