package main

// 末两段是合法的 GOOS_GOARCH 对 windows_amd64，linux 位于中间被忽略。
func init() { tokens = append(tokens, "M_LINUX_WINDOWS_AMD64") }
