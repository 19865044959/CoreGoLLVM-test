package main

// 末两段是合法的 GOOS_GOARCH 组合（windows + amd64），必须两者同时匹配。
func init() { mark("pair_windows_amd64") }
