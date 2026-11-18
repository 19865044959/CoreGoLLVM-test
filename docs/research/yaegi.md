# yaegi v0.16.1

- 来源：`research/repos/yaegi`（partial clone，仅 `*_test.go` / `_test/testdata` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/yaegi`
- 本地核验：`git describe` = `v0.16.1`，HEAD `3fbebb3`（2024-04-03，`fix: avoid memory leak in closure`）
- 模块：`github.com/traefik/yaegi`，`go 1.21`；仓库内 20 个 `*_test.go`。
- 范围：精读 `interp/{compile,interp_eval,interp_export,interp_file,interp_test,self_example,build,src}_test.go`、`extract/extract_test.go`、`internal/unsafe2`、`example/{getfunc,closure,fs}` 等 14 个文件。文件:行均相对本仓根目录。
- 说明：解释器实现源码未在 partial clone 中，只有测试；可从测试反推“AST→求值”的最小实现。所有用例用 `go/parser` + 自写求值器，不复制 yaegi 代码。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `interp/compile_test.go:12` | `parser.ParseFile` 得到 file，逐个 `ast.GenDecl`/`FuncDecl`/Block/Expr 调用 `CompileAST`；断言 import/type/var/const 的 token 类型 | L16 | 中 | 直接使用 `go/parser`、`go/token`、`go/ast`；自写 compile 分派 `ast.Node` 类型 |
| `interp/interp_eval_test.go:40` | 算术/比较/移位/一元表达式求值，含 untyped int/float 混合、除零、负数移位错误（至 `:75`） | L16/L11 | 中 | Pratt/递归下降解析 + `go/constant` 求值；错误文本表驱动 |
| `interp/interp_eval_test.go:76` | 常量移位、有符号/无符号转换（至 `:85`） | L11 | 中 | `go/constant.Shift` + 显式位宽/符号检查 |
| `interp/interp_eval_test.go:87` | 常量与变量混合、`const maxlen = cap(aa)` 的“常量定义循环”错误 | L11 | 中 | 常量表带求值状态（未解析/解析中/完成），环形依赖报错 |
| `interp/interp_eval_test.go:142` | 内建 `len/cap/append/copy/delete/make/new/panic/recover` 等（至 `:195`） | L16 | 高 | 用 `reflect` 实现内建；panic/recover 用真实 panic + defer recover |
| `interp/interp_eval_test.go:227` | 函数字面量、命名返回值、裸 return、`_` 非法使用 | L16 | 中 | AST FuncLit 求值成闭包；返回值槽位预分配 |
| `interp/interp_eval_test.go:324` | struct 的 func 字段与字面量函数字段调用 | L16/L06 | 中 | `reflect.StructOf`/普通 struct + `reflect.Value.Call` |
| `interp/interp_eval_test.go:396` | 复合字面量、数组/切片/map/struct（`:484`/`:500`/`:511`） | L16/L13 | 中 | 递归求值 CompositeLit；map 输出排序保证确定 |
| `interp/interp_eval_test.go:580` | 方法集：值/指针接收者、嵌入 struct、接口断言 `root.(Hi)` | L16/L06 | 高 | `reflect` 方法查找 + itab 模拟（接口值保存动态类型） |
| `interp/interp_eval_test.go:619` | chan + goroutine + 双向收发（至 `:652`） | L16/L02 | 高 | 真实 chan 直接执行；`go` 语句启动 goroutine |
| `interp/interp_eval_test.go:653` | 解释代码调用宿主传入的 `func(string) string` | L16/L06 | 中 | `reflect.Value.Call`；解释函数与 Go 函数统一为 `reflect.Value` |
| `interp/interp_eval_test.go:672` | 调用检查：实参不足、类型不符、变参 `...`、多返回值展开（至 `:714`） | L16 | 高 | 调用前按形参类型做 assignability 检查；变参打包 slice |
| `interp/interp_eval_test.go:732` | 解释出的接口类型对 `reflect.TypeOf` 的观测（`interp.valueInterface`） | L06/L16 | 高 | 动态接口用 `reflect.StructOf` + 包装类型模拟；记录动态类型 |
| `interp/interp_eval_test.go:755` | `Use` 导出 map 缺符号时返回错误而非 panic | L16/L06 | 中 | 导出符号表 `map[string]reflect.Value`，缺失即错误 |
| `interp/interp_eval_test.go:783` | `EvalWithContext` 取消 `for{}`/`select{}`/阻塞 chan/双 Lock（7 个子用例） | L16/L02 | 高 | 在 AST 循环/阻塞点插入 ctx 检查；取消后解释器仍可继续 Eval |
| `interp/interp_eval_test.go:935` | 多次 Eval 共享全局作用域、重声明规则 | L16 | 中 | 全局符号表 + 每次 Eval 的局部作用域链 |
| `interp/interp_eval_test.go:1175` | 两个 `EvalPath` 文件并发运行，stdout 交错但无竞态 | L16/L02 | 高 | 独立解释器实例并发；共享输出加锁 |
| `interp/interp_eval_test.go:1299` | 多 REPL 实例并发执行，校验包级全局变量不被写坏 | L16/L02 | 高 | 全局注册表只读；`Use` 时复制符号 map |
| `interp/interp_eval_test.go:1426` | REPL 逐行累积：括号/多行字符串/逗号未闭合时不报错，错误行号准确（至 `:1608`） | L16 | 高 | `parser.ParseExpr` 失败时判断“是否可续行”，维护行号偏移 |
| `interp/interp_eval_test.go:1819` | 解释器传递命令行参数；`:1830` restricted env | L16 | 中 | 显式配置结构体传入，不读进程全局 |
| `interp/interp_export_test.go:43` | `Use(Exports{...})` 语义校验（包名路径必须 `pkg/pkg`）；`:79` 解释类型实现宿主接口（Wrap 函数字段转发） | L16/L06 | 高 | 宿主接口 + 函数字段包装；解释方法赋给 `DoXxx` |
| `interp/interp_export_test.go:112` | 解释代码调用宿主变参方法 `func(...string)` | L16/L06 | 中 | `reflect.Value.CallSlice` 处理变参 |
| `interp/interp_file_test.go:26` | 批量运行 `_test/*.go`，从文件末尾注释读 `Output:`/`Error:`，比对 stdout/错误 | L16 | 高 | `parser.ParseFile(ParseComments)` 提取期望；`Eval` 后比较输出 |
| `interp/self_example_test.go:10` | 解释器自举：解释代码内再 `interp.Self.Eval` | L16 | 高 | 两级解释器实例 + 输出捕获，验证自引用 |
| `interp/interp_test.go:195` | `Globals()` 返回 `map[string]reflect.Value`；常量以 `go/constant.Value` 暴露 | L06/L16 | 中 | 全局符号表保存 reflect.Value；常量分支保存 constant.Value |
| `extract/extract_test.go:28` | 从真实包生成 `func init(){ Symbols["pkg"] = map[string]reflect.Value{...} }`；含数学常量 `constant.MakeFromLiteral` 与接口 wrapper | L16/L12/L10 | 高 | `go/importer` + `go/types` 枚举包对象，生成确定性注册代码 |
| `interp/build_test.go:13` | build tag 组合求值（linux/windows/go1.x/自定义 tag）；`:54` 文件名后缀过滤；`:92` 解析 Go 小版本 | L15 | 中 | `go/build.Context.MatchFile` 或自写 tag 表达式求值 |
| `internal/unsafe2/unsafe_test.go:10` | `reflect.StructOf` 造结构体，再改写字段类型为指针类型 | L06/L07 | 高 | 仅用 `reflect.StructOf` 造动态结构体；不改写字段（标准库做不到） |
| `example/getfunc/getfunc_test.go:11` | `Eval("bar.NewFoo")` 取函数值，`reflect.Value.Call(nil)` 调用 | L16/L06 | 低 | 符号表返回 `reflect.Value`，直接 Call |
| `example/closure/closure_test.go:10` | 解释函数返回闭包，闭包再返回闭包，跨宿主调用 | L16/L06 | 中 | 解释函数的自由变量以 `reflect.Value` 捕获于闭包 |
| `example/fs/fs_test.go:52` | `fstest.MapFS` 作为源码文件系统加载多包 | L16 | 中 | `io/fs` 抽象；`fs.ReadFile` 读取虚拟文件 |

## 推荐收录（15 条）

1. `l16_yg_compile_ast` — `interp/compile_test.go:12`。`go/parser`→`go/ast` 逐节点编译；P1，L16。
2. `l16_yg_eval_expr` — `interp/interp_eval_test.go:40`、`:76`。表达式求值与 untyped 常量规则；P0，L16/L11。
3. `l11_yg_const_fold` — `interp/interp_eval_test.go:87`、`interp/interp_test.go:195`。常量传播/定义循环检测、`go/constant` 暴露；P1，L11。
4. `l16_yg_struct_method` — `interp/interp_eval_test.go:324`、`:580`。struct/方法集/嵌入/接口断言；P1，L16/L06。
5. `l16_yg_call_check` — `interp/interp_eval_test.go:672`。实参/变参/多返回值调用校验；P1，L16。
6. `l06_yg_reflect_iface` — `interp/interp_eval_test.go:732`、`interp/interp_export_test.go:79`。解释类型与宿主接口互操作；P2，L06/L16。
7. `l16_yg_context_cancel` — `interp/interp_eval_test.go:783`。7 种阻塞形态可取消且解释器可复用；P2，L16/L02。
8. `l16_yg_repl` — `interp/interp_eval_test.go:1426`。多行不完整输入的续行判定与错误行号；P1，L16。
9. `l16_yg_multi_eval` — `interp/interp_eval_test.go:935`。多次 Eval 的全局/局部作用域；P1，L16。
10. `l16_yg_self_interp` — `interp/self_example_test.go:10`。解释器自举；P2，L16。
11. `l16_yg_extract_wrapper` — `extract/extract_test.go:28`。`go/types` 枚举包符号 + 接口 wrapper 生成；P2，L16/L12/L10。
12. `l12_yg_symbols_registry` — `extract/extract_test.go:14`（生成 `init()` 注册 `Symbols`）、`interp/interp_export_test.go:43`。init 注册表 + `Use` 导入；P1，L12。
13. `l06_yg_dynamic_struct` — `internal/unsafe2/unsafe_test.go:10`。`reflect.StructOf` 动态结构体与字段类型布局；P2，L06。
14. `l16_yg_concurrent_eval` — `interp/interp_eval_test.go:1175`、`:1299`。并发解释/共享注册表只读；P2，L16/L02。
15. `l16_yg_file_harness` — `interp/interp_file_test.go:26`。从源码尾部注释提取 golden `Output:`/`Error:`；P1，L16。

## 不可移植清单

- `EvalPath`/`interp_file_test.go`：依赖 `_test/` 目录树与 `GOPATH` 布局（partial clone 仅部分 testdata）。
- `interp_consistent_test.go:26`：需要调用本机 `go run` 编译同一批 `_test/*.go` 并比对输出，依赖真实 Go 工具链。
- `self_example_test.go`：自举需完整 yaegi 源码、`interp.Symbols` 与 `stdlib.Symbols`。
- `extract`：`go/importer` 加载真实包、向工作目录写生成文件；生成结果依赖 Go 版本与包集合。
- `stdlib.Symbols`：上万条符号表，不可复制；只能用少量自建 `Exports` 替代。
- `example/pkg`、`interp/src_test.go:50`：依赖 GOPATH/vendor 目录和文件系统路径解析。
- `example/fs`：虽然用 `fstest.MapFS`，但 `NewSample` 等包装与 yaegi 内部 FS 耦合，只保留 `io/fs` 机制。
- `internal/unsafe2`：实现源码不在 partial clone，且依赖 `unsafe` 改写 `reflect.StructOf` 字段（非标准可移植技巧）。
- `interp/build_test.go:13` 的 `build.Context.ReleaseTags` 固定为 `go1.11`，只能借表达式语义，不能照搬版本常量。
- `interp_eval_test.go` 中的 imports/`stdlib`/http 用例依赖真实标准库符号表与网络/文件环境，改用自建小符号集。
