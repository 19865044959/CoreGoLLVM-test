package b

// 文件名后缀 _amd64：仅 amd64 参与编译。amd64 下 b 不导入 a。
func Edge() string { return "b-standalone" }
