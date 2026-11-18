//go:build neverParsedScope

package otherpkg

// 该文件同时被 tag 与包名检查排除；embed 模式还含非法的 .. 路径。
// 只要构建成功，就说明排除发生在包体、包名与 embed 解析之前。
import _ "embed"

//go:embed ../outside/*.bin
var payload []byte

func broken( {
