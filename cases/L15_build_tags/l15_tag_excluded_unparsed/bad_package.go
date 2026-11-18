//go:build neverParsedPackage

package otherpkg

// 被 tag 排除的文件不参与同目录包名一致性检查。
var baseInits = 100

func baseImpl() string { return "other-package" }
