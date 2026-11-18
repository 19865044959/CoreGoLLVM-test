# hugo v0.155.0

- 来源：`research/repos/hugo`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/hugo`
- 本地核验：`git describe` = `v0.155.0`，HEAD `74c0bbf`（2026-01-28，`releaser: Bump versions for release of 0.155.0`）
- 模块：`github.com/gohugoio/hugo`；仓库内 374 个 `*_test.go`。
- 范围：按 `template|FuncMap|Execute|reflect|parser|register|init|partial` 筛选后精读约 20 个文件，重点为 `tpl/internal/go_templates/**`、`tpl/tplimpl`、`tpl/templates`、`hugolib/template_test.go`、`codegen`、`common/hreflect`、`tpl/collections`。文件:行均相对本仓根目录。
- 注意：`tpl/internal/go_templates/{texttemplate,htmltemplate}` 是 Hugo 内嵌的 Go 标准库模板分支，测试源自 Go 上游（BSD）；只能引用场景，用例须从零用 stdlib 重写，不复制测试或实现。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `tpl/internal/go_templates/texttemplate/exec_test.go:299` | `.U.V` 连续两层指针字段求值 | L16/L06 | 低 | 自写 `evalField`：`reflect.Indirect` 沿链 `FieldByName`，nil 提前报错 |
| `.../texttemplate/exec_test.go:305` | map 缺 key 输出 `<no value>`；`:465` 缺 key 在 if 中为假 | L16 | 低 | `MapIndex` + `IsValid` 分支，区分“零值”与“不存在” |
| `.../texttemplate/exec_test.go:342` | `Stringer`/`Error` 接口值渲染；`:404` typed nil 接口仍可调用方法 | L16/L06 | 中 | 接口断言 + 方法值调用；typed nil 不丢方法集 |
| `.../texttemplate/exec_test.go:359` | `any` 中 int/string/slice/struct 及嵌套字段递归取值（至 `:364`） | L16/L06 | 中 | `reflect.Kind` switch + `Elem`，逐层解包空接口 |
| `.../texttemplate/exec_test.go:403` | `[]*T{nil}` 上 range 后调用方法不 panic | L16/L06 | 中 | 遍历 nil 元素时保留类型信息，方法调用走接口/方法值 |
| `.../texttemplate/exec_test.go:411` | `call` 内建：函数字段、变参、nil/非函数/多返回值错误（至 `:418`） | L16 | 中 | 自实现 `call`：检查 Kind 为 Func、参数个数、变参打包与返回值校验 |
| `.../texttemplate/exec_test.go:452` | typed nil 接口在 if 中为假；`:519` and/or 对未定义值短路 | L16/L11 | 中 | 自写 `isTrue(reflect.Value)`；逻辑节点按 Go 语义短路，未选分支不求值 |
| `.../texttemplate/exec_test.go:526` | slice/map index 越界、接口字段索引；`:553` 三下标切片错误 | L16 | 中 | `reflect.Index/Slice` + 显式 len/cap 边界检查，错误文本固定 |
| `.../texttemplate/exec_test.go:603` | range slice/map、双变量、break/continue（至 `:620`） | L16 | 中 | 自写 Range 节点；map 键先排序保证输出确定 |
| `.../texttemplate/exec_test.go:625` | range over `iter.Seq[int]`/`iter.Seq2` 及 else 分支（至 `:635`） | L16/L01 | 高 | 手写 `func(yield func(T) bool)` 迭代器协议，执行 range-over-func |
| `.../texttemplate/exec_test.go:684` | `*interface` 与接口字段上的方法调用（bug1/bug2，`:686`） | L16/L06 | 高 | 控制 Indirect 层数，接口值先 Elem 再按方法集选值/指针接收者 |
| `.../texttemplate/exec_test.go:695` | 函数实参指针/值间接化（bug6a–d）；`:711` nil 指针实参不得 panic | L16/L06 | 中 | 按形参类型 Elem/Addr 调整实参；recover 后转 `error` |
| `.../texttemplate/exec_test.go:1561` | nil 指针/空 map 上取字段的错误分类 | L16 | 中 | 自写 fieldError：区分 nil pointer、missing field、nil map |
| `.../texttemplate/exec_test.go:1622` | 自递归模板触发“最大执行深度” | L16/L17 | 中 | 执行栈深度计数器，超限返回错误而不是栈溢出 |
| `.../texttemplate/exec_test.go:1638` | Execute 数据为 `reflect.Value`，index 结果可寻址并调 String 方法 | L16/L06 | 高 | Execute 接受 `reflect.Value`；index 结果 Addr 后再取方法 |
| `.../texttemplate/exec_test.go:1735` | 函数/方法 panic 包装成执行错误；`:1796` call 参数个数检查 | L16 | 中 | recover 包装为 `error`；调用前校验 arity 与参数可赋值性 |
| `tpl/internal/go_templates/htmltemplate/exec_test.go:830` | html/template 执行与自动转义；`escape_test.go:36` 各上下文转义表 | L16 | 高 | 直接使用 stdlib `html/template` 重写；或手写 HTML/URL/JS/CSS 上下文状态机 |
| `.../htmltemplate/exec_test.go:1737` | 多模板并发 Execute 与转义缓存竞争 | L16/L02 | 高 | `sync.Mutex`/`atomic` 保护转义缓存，`-race` 下并发执行 |
| `tpl/internal/go_templates/htmltemplate/clone_test.go:184` | Clone 后 FuncMap 仍生效；`:205` Clone 与 Execute 并发 | L16/L02 | 高 | 深拷贝模板树 + 函数表；复制后并发执行校验输出 |
| `tpl/internal/go_templates/texttemplate/multi_test.go:97` | 嵌套 `define`/`template`、`.U` 与变量作用域传递（至 `:99`） | L16 | 中 | 模板名→节点表；执行时维护 dot 与变量栈 |
| `tpl/internal/go_templates/htmltemplate/multi_test.go:266` | 空 block 的替换结果受模板名 map 构建顺序影响（Issue19294） | L12/L16 | 高 | 注册表按依赖顺序解析；循环 100 次打乱插入顺序验证 |
| `tpl/internal/go_templates/texttemplate/hugo_template_test.go:70` | 自定义 Executer 钩子：GetFunc/GetMethod/GetMapValue 用 reflect 改写查找 | L16/L06 | 高 | 定义 execHelper 接口，函数/方法/字段查找全部走 `reflect.Value` |
| `codegen/methods_test.go:27` | 反射遍历接口方法集与签名，生成 JSON 调用代码（`MethodsFromTypes`/`ToMarshalJSON`） | L16/L06/L10 | 高 | `reflect.Type.Method` 遍历、签名格式化、方法名排序后生成源码 |
| `common/hreflect/helpers_test.go:34` | IsTruthful/IsZero 语义；`:99` IsNil；`:113` Indirect（双指针、接口、带方法结构体） | L06 | 中 | `reflect` 递归解引用；`IsZero` 接口优先级；nil 判定表驱动 |
| `tpl/collections/where_test.go:683` | reflect.Value 比较与 `in/intersect`；`:814` evaluateSubElem 取字段/方法/Stringer | L06/L11 | 高 | 手写比较器与子元素求值：类型归一化、可寻址性、方法返回值校验 |
| `tpl/internal/templatefuncRegistry_test.go:29` | 方法值→函数名（`runtime.FuncForPC`），区分 gccgo thunk | L16/L12 | 低 | `runtime.FuncForPC` + 名称裁剪，固定命名规则 |
| `tpl/tplimpl/template_funcs_test.go:27` | 遍历 `TemplateFuncsNamespaceRegistry`，执行所有注册函数示例 | L12/L16 | 高 | 自建 FuncMap 注册表，多个 `init()` 注册命名空间，逐项执行 |
| `hugolib/template_test.go:283` | partial 返回值与 dollarContext（`:330` 零值参数、`:365` partialCached 键） | L16 | 中 | 自写 partial：函数表查找 + 缓存 map + 参数传递 |
| `tpl/templates/templates_integration_test.go:99` | `try` 包裹错误；`:234`/`:250` 模板 parse/execute 错误文本 | L16 | 中 | 错误包装结构体；固定错误串比较 |
| `tpl/tplimpl/templatestore_integration_test.go:1414` | partial 无限递归被 depth 限制拦截 | L16 | 低 | 与递归模板同一深度计数机制 |

## 推荐收录（15 条）

1. `l16_tpl_field_chain` — `tpl/internal/go_templates/texttemplate/exec_test.go:299`。嵌套指针字段 + 缺 key + nil map 求值；P1，L16/L06。缩为 3 层结构体。
2. `l16_tpl_missing_key_truth` — `.../exec_test.go:305`、`:452`、`:519`。缺 key/typed nil/短路三合一；P0，L16/L11。
3. `l16_tpl_typed_nil_method` — `.../exec_test.go:404`、`:1661`。typed nil 接口方法、index/call 接口值；P1，L16/L06。
4. `l16_tpl_call_builtin` — `.../exec_test.go:411`、`:1796`。call 函数字段/变参/arity 错误；P1，L16。
5. `l16_tpl_index_slice` — `.../exec_test.go:526`、`:553`。index/slice 越界与三下标；P1，L16。
6. `l16_tpl_range_iter` — `.../exec_test.go:603`、`:625`。range slice/map + iter.Seq/Seq2，map 排序保证确定；P1，L16/L01。
7. `l16_tpl_nested_define` — `.../texttemplate/multi_test.go:97`、`htmltemplate/multi_test.go:266`。嵌套 define 与空 block 顺序；P2，L16/L12。
8. `l16_tpl_recursion_panic` — `.../exec_test.go:1622`、`:1735`、`templatestore_integration_test.go:1414`。递归深度与 panic 包装；P1，L16。
9. `l16_html_escape_context` — `htmltemplate/exec_test.go:830`、`escape_test.go:36`。HTML/URL/JS/CSS 上下文自动转义；P2，L16。
10. `l16_html_clone_funmap` — `htmltemplate/clone_test.go:184`、`:205`。Clone 后 FuncMap 与并发执行；P2，L16/L02。
11. `l16_tpl_custom_executor` — `texttemplate/hugo_template_test.go:70`。反射钩子替换函数/方法/字段查找；P2，L16/L06。
12. `l06_hreflect_truth_indirect` — `common/hreflect/helpers_test.go:34`、`:99`、`:113`。IsTruthful/IsNil/Indirect 全类型矩阵；P1，L06。
13. `l06_codegen_methodset` — `codegen/methods_test.go:27`。反射方法集→确定性源码生成；P2，L06/L16/L10。
14. `l12_tpl_funcs_registry` — `tpl/tplimpl/template_funcs_test.go:27`、`tpl/internal/templatefuncRegistry_test.go:29`。init 注册 FuncMap 命名空间并逐项执行；P2，L12/L16。
15. `l11_tpl_const_shortcircuit` — `.../exec_test.go:289`、`:294`、`:511`。常量类型判定、溢出报错、and/or 短路（未选分支不执行）；P1，L11/L16。

## 不可移植清单

- `hugolib/**` 集成测试：依赖 txtar 内存站点 + `NeedsOsFS` + `public/` 输出目录，无法落入单包 main。
- `modules/**`、`tpl/data`：需要网络下载模块/远程数据。
- `resources/resource_transformers/**`（postcss/dartsass/esbuild）：依赖外部二进制与 node 生态。
- `markup/**`、image 处理：依赖 goldmark/chroma 等三方库及图片样本。
- `codegen`：`NewInspector` 用 go/packages 加载整个 hugo 模块；只能借鉴“反射方法集→源码”思路。
- `tpl/tplimpl/template_funcs_test.go:27`：执行全部 Hugo FuncMap 实现需完整站点构建，注册表机制可移植、具体函数不可。
- `tpl/internal/go_templates/testenv`、`ParseFiles/ParseGlob/ParseZipFS`：依赖宿主文件系统/zip testdata。
- 模板 store layout 查找类（`templatestore_integration_test.go:151` 等）：依赖 Hugo layout/theme 配置树。
- `hugolib/embedded_templates_test.go`：依赖 `go:embed` 的完整内建模板资源。
- 全部测试使用 `quicktest`；用例改为标准库 `testing` + 显式比较。
