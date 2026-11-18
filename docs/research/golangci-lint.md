# golangci-lint v2.0.2

- 来源：`research/repos/golangci-lint`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/golangci-lint`
- 本地核验：`git describe` = `v2.0.2`，HEAD `2b224c2`（2025-03-25，`chore: prepare release`）
- 模块：`github.com/golangci/golangci-lint/v2`，`go 1.23.0`；仓库内 189 个 `*_test.go`。
- 范围：按 `ast|types|nolint|analyzer|registry|linter|config|unconvert|printf` 筛选后精读约 18 个测试/testdata 文件，重点为 `pkg/golinters/nolintlint`、`pkg/lint/lintersdb`、`pkg/result/processors`、`pkg/golinters/{copyloopvar,unconvert,staticcheck,intrange,ineffassign,usestdlibvars,govet,gocritic}`、`pkg/config`、`test/`。文件:行均相对本仓根目录。
- 说明：绝大多数 linter 测试是 `//golangcitest:args` 驱动的集成 harness（go/packages + 真实模块）；可迁移的是 testdata 中的“源码→期望诊断”语义，用 `go/parser`/`go/types` 在单包里复刻最小规则。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `pkg/golinters/nolintlint/internal/nolintlint_test.go:16` | 源码字符串 → AST → 期望 `result.Issue` 列表（text/Pos/SuggestedFix），`:281` `parser.ParseFile(ParseComments)` | L16 | 高 | `go/parser` 解析注释；遍历 `ast.CommentGroup` 匹配 `//nolint`，计算 `token.Position` 与 TextEdit |
| `pkg/golinters/nolintlint/testdata/nolintlint.go:9` | `//nolint` 缺具体 linter、缺解释、多余空格三类诊断（至 `:11`） | L11/L16 | 低 | 正则匹配指令语法；固定错误文本表驱动 |
| `pkg/golinters/copyloopvar/testdata/copyloopvar.go:12` | Go 1.22 起 `i := i` 冗余拷贝可删除（`:16`/`:32` 同理） | L11/L01 | 中 | 用 `go/ast` 识别 range body 内同名短声明；按语言版本开关规则 |
| `pkg/golinters/unconvert/testdata/unconvert.go:30` | 函数类型转换 `F(F(nil))` 冗余；`:75`–`:90` 全部基础类型恒等转换 | L11/L16 | 中 | `go/types.Info.Types[expr].Type` 比较转换前后类型是否 identical；保留未类型化常量转换 |
| `pkg/golinters/usestdlibvars/testdata/usestdlibvars.go:12` | 字面量 `200` 可替换为 `http.StatusOK` | L11 | 低 | 识别 `WriteHeader` 调用的常量实参；`go/constant.Int64Val` 取值映射 |
| `pkg/golinters/staticcheck/testdata/staticcheck.go:11` | 自赋值 `x = x`；`:22` Printf 格式串与实参类型不匹配（SA5009） | L16/L11 | 中 | AST 赋值节点同 ident 判定；`go/types` 取实参类型，解析格式串动词 |
| `pkg/golinters/intrange/testdata/intrange.go:9` | `for i:=0; i<10; i++` 可改整数 range；`:12` 上界为常量 `math.MaxInt8` | L11 | 中 | AST for 语句形状匹配 + `types.Info` 常量值/类型检查，排除循环体修改 i |
| `pkg/golinters/ineffassign/testdata/ineffassign.go:10` | 赋值后未被读取的 dead store | L11 | 中 | 简化数据流：记录每个 ident 的写点与后续读点，块内覆盖即报告 |
| `pkg/golinters/decorder/testdata/decorder.go:1` | 声明顺序（const→var→type→func→init）与 `expected_exitcode 0` | L12/L16 | 中 | AST 顶层声明按 token 排序检查；`init()` 必须最后 |
| `pkg/golinters/govet/testdata/govet.go:29` | printf 动词/实参类型不匹配；`:34` `string(int)` 得到单 rune 而非数字串 | L11/L16 | 中 | `types.Info` 类型 + 格式串解析；字符串转换来源类型判定 |
| `pkg/golinters/govet/govet_test.go:19` | 默认 analyzer 必须全部出现在 all 列表；`:46` 列表按名排序；`:61` enable/disable 矩阵 | L12 | 中 | 注册表 + 名称排序断言；配置开关逐项验证 |
| `pkg/lint/lintersdb/manager_test.go:16` | 配置 enable/disable → enabled linters map（`:66` build，`:152` 按 LoadMode 合并 analyzer） | L12 | 高 | 自建 linter 注册表（name→构造函数），按 config 求集合；LoadMode 位或合并 |
| `pkg/lint/lintersdb/validator_test.go:65` | 非法 linter 名/互斥项校验错误（`:84`、`:105`、`:121`） | L12 | 低 | 校验器遍历注册表，未知名称返回确定错误 |
| `pkg/result/processors/nolint_filter_test.go:47` | 行号 → nolint 指令区间匹配；`:182` `ignoredRange.doesMatch` 边界 | L16/L13 | 高 | 解析指令为 `{start,end,linters}` 区间表；区间命中算法表驱动 |
| `pkg/golinters/gocritic/gocritic_test.go:19` | 按 default/tags/checks 推导启用检查集合（`:276` Validate） | L12 | 中 | 检查器注册表带 tag；集合交并差运算，排序后比较 |
| `pkg/golinters/gosec/gosec_test.go:12` | 用户设置 → `gosec.Config` 全局/规则级映射 | L12 | 低 | 配置结构体到 `map[string]any` 的确定性转换 |
| `test/enabled_linters_test.go:18` | 配置/命令行优先级：disable/enable/fast/default | L12 | 中 | 配置合并顺序：default < config < CLI；结果排序断言 |
| `test/configuration_file_test.go:17` | 遍历全部测试配置做 schema 校验（`:22` linter 配置） | L12 | 中 | 用 `encoding/json`/`yaml` 解析到结构体，未知字段报错 |
| `test/testdata/configs/custom_linter_goplugin.yml:7` | 自定义 linter 类型 `goplugin`（`.so`）与 `module` | L12 | 高 | 注册表插入自定义构造器；`plugin.Open` 不可移植，只验证配置解析与优先级 |
| `pkg/config/config_test.go:9` | `IsGoGreaterThanOrEqual` 版本比较；`:87` `parseGoVersion` | L11 | 中 | 手写 `go1.x.y` 解析与语义比较，覆盖 pre-release |
| `pkg/goanalysis/pkgerrors/extract_test.go:8` | 多段嵌套 `go/packages` 错误栈裁剪到最内层位置 | L16 | 低 | 括号配对扫描字符串，提取最深 `file:line:col` |
| `test/linters_test.go:14` | `RunTestdata` 全量 linter 集成；`:21` typecheck 目录 | L12/L16 | 高 | 仅借“testdata + 期望诊断”结构，规则用单包 AST 复刻 |
| `test/fix_test.go:10` | 诊断附带 SuggestedFix，`in/`→`out/` 文件重写 | L16 | 中 | `token.FileSet` + `ast` 修改后 `go/format` 输出，字符串比较 |

## 推荐收录（15 条）

1. `l16_gcl_nolint_ast` — `pkg/golinters/nolintlint/internal/nolintlint_test.go:16`、`:281`。源码→AST→诊断位置/TextEdit；P1，L16。
2. `l11_gcl_copyloopvar` — `pkg/golinters/copyloopvar/testdata/copyloopvar.go:12`。Go 1.22 冗余循环变量拷贝；P1，L11/L01。
3. `l11_gcl_unconvert` — `pkg/golinters/unconvert/testdata/unconvert.go:30`、`:75`。类型恒等转换删除与常量例外；P1，L11/L16。
4. `l11_gcl_usestdlibvars` — `pkg/golinters/usestdlibvars/testdata/usestdlibvars.go:12`。常量值→标准库符号映射；P1，L11。
5. `l16_gcl_staticcheck` — `pkg/golinters/staticcheck/testdata/staticcheck.go:11`、`:22`。自赋值 + printf 类型检查；P1，L16/L11。
6. `l11_gcl_intrange` — `pkg/golinters/intrange/testdata/intrange.go:9`、`:12`。循环形状 + 常量上界；P1，L11。
7. `l11_gcl_ineffassign` — `pkg/golinters/ineffassign/testdata/ineffassign.go:10`。死存储数据流；P1，L11。
8. `l12_gcl_decorder` — `pkg/golinters/decorder/testdata/decorder.go:1`。声明顺序与 init 位置；P1，L12/L16。
9. `l12_gcl_linter_registry` — `pkg/lint/lintersdb/manager_test.go:16`、`:66`、`:152`。注册表+配置求集合+LoadMode 合并；P2，L12。
10. `l12_gcl_govet_analyzers` — `pkg/golinters/govet/govet_test.go:19`、`:46`、`:61`。analyzer 注册表完备性/排序/开关；P2，L12。
11. `l12_gcl_gocritic_tags` — `pkg/golinters/gocritic/gocritic_test.go:19`。tag/check 集合推导；P2，L12。
12. `l16_gcl_nolint_ranges` — `pkg/result/processors/nolint_filter_test.go:47`、`:182`。指令区间匹配与边界；P2，L16/L13。
13. `l12_gcl_config_precedence` — `test/enabled_linters_test.go:18`。default/config/CLI 合并优先级；P2，L12。
14. `l12_gcl_plugin_config` — `test/testdata/configs/custom_linter_goplugin.yml:7`。自定义 linter 注册与配置解析；P2，L12。
15. `l11_gcl_goversion` — `pkg/config/config_test.go:9`、`:87`。Go 版本解析比较，规则按版本启用；P1，L11。

## 不可移植清单

- 所有 `pkg/golinters/*/*_integration_test.go`：依赖 `test/testshared/integration` harness，以 go/packages 加载真实模块并运行第三方 analyzer；不能作为单包用例。
- `test/**`：要求先构建 golangci-lint 二进制、完整 `test/testdata` 树和特定 cwd；`TestSourcesFromTestdata` 还会按目录派发。
- `custom_linter` 的 `goplugin`：依赖 `plugin.Open` 加载 `.so`，平台/构建模式受限；`module` 型需外部模块。
- 第三方 linter 实现（staticcheck/gosec/gocritic/revive/go-critic）：只借 testdata 语义，规则用 stdlib AST 重写。
- `test/fix_test.go`：会实际改写 `testdata/fix/in` 文件并要求与 `out` 完全一致，属文件系统副作用。
- `pkg/commands/**`、`pkg/printers`：CLI/输出格式，与本维度无关且依赖 cwd/版本信息。
- `scripts/website/expand_templates`：依赖 docs 目录与模板站点。
- `testdata` 中 cgo 文件（`*_cgo.go`）：需要 CGO 环境，不能进纯 stdlib 用例。
