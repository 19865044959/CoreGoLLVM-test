# ent v0.14.4

- 来源：`research/repos/ent`（partial clone，仅测试文件与少量源码/testdata；commit 088bfd75，tag v0.14.4，module `entgo.io/ent`，go.mod `go 1.23`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/ent`
- 重点维度：L01 泛型（少，需提炼）、L06 反射/接口、L10 代码生成、L11 编译期优化、L12 初始化
- 说明：ent 的生成物是大量 struct + 方法集 + 常量表 + 谓词类型树；partial clone 中生成后的 `entc/integration/ent`、`examples/*/ent` 不在，只能在测试中观察"生成器输入/输出契约"。集成测试绝大多数依赖真实数据库与 cgo sqlite，只借场景不照搬。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| entc/gen/graph_test.go:68 | 由 Schema/Edge 描述构造 Graph，校验节点顺序、边类型、可选/唯一属性 | L10 | 中 | 定义 schema 描述 struct，用确定性排序构造节点/边图，逐项断言 |
| entc/gen/graph_test.go:449 | `TestGraph_Gen`：按 feature flag 生成 `ent.go/client.go/<type>_{create,update,delete,query}.go` 等文件集；关闭 feature 后对应文件不再产出 | L10/L11 | 高 | text/template 生成 6 类文件；用「feature 表 → 输出文件集合」验证 DCE 式裁剪，非生成文件必须不存在 |
| entc/gen/graph_test.go:565 | `TestGraph_Hooks`：Generator 中间件链，遍历生成元数据并用 `reflect.StructTag` 校验每个字段的 tag | L10/L06 | 中 | 手写 generator 接口 + middleware 链；在生成前用 reflect.StructTag 扫描字段 tag，缺失即失败 |
| entc/gen/graph_test.go:588 | `TestDependencyAnnotation_Build`：按 `reflect.Slice` 等 Kind 与类型字符串（`*http.Client`/`[]*url.URL`）推导注入字段名 | L06/L10 | 低 | 用 reflect.Kind + 类型字符串前缀/后缀规则，表驱动生成字段名并断言 |
| entc/gen/bench_test.go:18 | `BenchmarkGraph_Gen`：LoadGraph 后重复执行 `graph.Gen()`，模板解析一次、渲染多次 | L10/L11 | 中 | 预解析模板 + 固定 schema 循环渲染；用文件数量/内容校验渲染稳定性 |
| entc/integration/template/template_test.go:24 | 自定义模板注入客户端字段（`HTTPClient`），再通过生成代码访问；另用 reflect 检查生成 client 的未导出字段 | L10/L06 | 高 | 自定义 text/template 片段合并进主模板；生成后 go/parser 校验字段与方法存在 |
| entc/gen/type_test.go:89 | 类型名到 label/复数/表名的规范化 | L10 | 低 | 常量改名表：CamelCase→snake、复数化规则，表驱动断言 |
| entc/gen/type_test.go:124 | 生成代码 receiver 名（首字母/小写）规则 | L10 | 低 | 单字符 receiver 生成规则表，覆盖缩写与冲突 |
| entc/gen/type_test.go:147 | 枚举字段名到常量名（`RoleAdmin` 等）的映射 | L10 | 低 | 单词切分 + 大写化常量表，非法字符过滤 |
| entc/gen/type_test.go:252 | `FieldConstant`：`user_id`→`FieldUserID` 等生成常量名 | L10/L11 | 低 | 表驱动 snake→Camel 生成；输出作为 const 名参与后续模板 |
| entc/gen/type_test.go:299 | BuilderField：Go 关键字/冲突字段加下划线（`type`→`_type`、`SSOCert`→`_SSOCert`） | L10 | 低 | 关键字/首字母大写冲突表，生成合法标识符并 go/parser 复验 |
| entc/gen/globalid_test.go:72 | GlobalID 增量起点注册表：必须为 1<<32 倍数、冲突时按冲突标记恢复 | L12/L10 | 中 | 自建 table→offset 注册表；parse 冲突标记 → 保留远端条目 → 新表取下一个 1<<32 |
| entc/gen/globalid_test.go:116 | `must[T any](t T, err error) T` 泛型测试辅助 | L01 | 低 | 泛型 must 帮助函数，分别实例化 int/string/struct/map 并断言 panic 路径 |
| entc/integration/hooks/hooks_test.go:724 | `TestSharedInterceptor`：泛型 interceptor 复用于不同 query 类型 | L01/L06 | 中 | 定义 `Limiter[Q interface{ Limit(int) }]`，以接口约束调用 Limit；用两个实现类型验证方法集派发 |
| entc/integration/hooks/hooks_test.go:755 | `TestTypedTraverser`：生成代码的强类型遍历方法（`QueryPets`）链式调用 | L01/L10 | 中 | 手写 ent 风格强类型 builder（`UserQuery.QueryPets() *PetQuery`），验证链式返回类型与方法集 |
| entql/types_test.go:14 | `TestFielder`：谓词表达式树（And/Or/Not/EQ/GT）实现统一 Fielder 接口并渲染字符串 | L06/L16 | 低 | 自建 `Predicate` 接口 + 组合节点；树遍历生成确定字符串 |
| entql/entql_test.go:16 | `TestPString`：字段/边谓词渲染，含 `has_edge(...)`、`in [...]`、`not in`、nil 比较、浮点常量 | L06/L16 | 低 | 小型表达式 AST + 递归 String()；覆盖嵌套边与否定 |
| entc/integration/entql_test.go:21 | EntQL 过滤注入 Query/Mutation，按字段/边过滤后继续链式查询 | L06/L13 | 中 | 内存表 + 谓词接口解释执行；校验过滤、更新、边遍历结果 |
| schema/field/field_test.go:748 | 枚举 Descriptor：枚举值表、默认值、GoType（Stringer/ValueScanner）能力探测 | L06/L10 | 中 | 枚举描述 struct + reflect 探测 `Value()/Scan()/String()` 方法集，生成常量表 |
| schema/field/field_test.go:915 | `TestRType_Implements`：用 `reflect.TypeOf((*iface)(nil)).Elem()` 检查值/指针接收者是否实现接口 | L06 | 低 | 定义两个组合接口，分别用值/指针类型检查 Implements；覆盖只实现一半方法的类型 |
| schema/field/field_test.go:941 | Type 枚举到字符串表，越界值返回 `"invalid"` | L10/L11 | 低 | 常量表 + 边界值（0/21）断言，验证越界分支 |
| dialect/sql/scan_test.go:18 | `TestScanSlice`：反射扫描进 `[]struct`，支持列名→字段名、`json`/`sql` tag、`COUNT(*)` 特殊列、`[]*struct` | L06/L13 | 中 | 自建列数据源 + reflect 遍历字段；实现 tag 优先、大小写/下划线归一、忽略无匹配列 |
| dialect/sql/scan_test.go:219 | 嵌套/内嵌 struct、`**string`、`*int` 的反射赋值与 nil 语义 | L06 | 中 | 递归 deref/分配指针链，内嵌字段提升；验证 nil 列留零值 |
| dialect/sql/scan_test.go:252 | `[]*string`、`[]**int`、`[]*struct{Age *int; Name **string}` 的指针切片扫描 | L06/L05 | 中 | 每行新建元素并递归分配指针层级，断言值与 nil |
| dialect/sql/scan_test.go:338 | 扫描进 `[]driver.Value`/`any`：保留底层驱动类型（string vs int64） | L06/L13 | 低 | 用 `[]any` 接收任意列，断言类型；比较 driver.Value 语义 |
| dialect/gremlin/encoding/graphson/marshaler_test.go:20 | Marshaler 接口 + typed-nil（`Marshaler(nil)`、函数返回指针）判空后编码 | L06/L07 | 中 | 自定义 Marshaler 接口；用 reflect2 风格判 typed-nil，替换为 `reflect.Value.IsNil` 手写 |
| dialect/gremlin/encoding/graphson/marshaler_test.go:62 | Unmarshaler 接口解码、错误传播、错误输入 | L06 | 中 | 接口断言 + 错误包装；坏输入不得 panic |
| dialect/gremlin/encoding/graphson/struct_test.go:15 | struct tag 编解码、嵌套 struct、omitempty/null 语义 | L06/L13 | 中 | reflect 遍历字段 + tag 解析；手写 JSON 词法/对象构造，覆盖嵌套与空值 |
| privacy/privacy_test.go:18 | Policy 接口：DecisionContext 注入，`Policies{}.EvalMutation` 返回 Deny | L06 | 低 | 定义 Policy 接口与方法集；context 携带 decision；空策略集合行为 |
| privacy/privacy_test.go:39 | `NewPolicies` 组合多个 policy，查询/变更两条路径调用次数不同 | L06 | 低 | 函数式接口 + 切片组合；用计数器验证 EvalQuery/EvalMutation 的调用序 |
| entc/load/schema_test.go:155 | `TestMarshalSchema`：mixin/annotation 经接口 `Merge` 合并后序列化为稳定 JSON | L06/L10 | 中 | 定义 Annotation 接口 + Merge；编码 JSON 前按 key 排序，golden 比较 |
| cmd/internal/printer/printer_test.go:17 | `gen.Graph` 渲染为确定性的 ASCII 表（字段/边两张表） | L10 | 低 | 自写列宽计算与表格打印；输出逐字节比较 |
| entc/internal/snapshot_test.go:20 | 从 snapshot 恢复生成文件、清理冲突标记，再触发 `go generate` | L10/L12 | 中 | 自写 snapshot 恢复器：按注释边界还原生成块；不调用外部 go generate |
| entc/load/load_test.go:15 | 用 go/packages 加载 schema 包、支持 build tag 与多 schema 目录 | L10/L15 | 中 | 用 go/parser + go/build 只解析目标目录，模拟包加载与 tag 过滤 |
| dialect/sql/sql_test.go:51 | 谓词构造函数输出 SQL 片段（EQ/In/Contains/And/Or/Not） | L10/L11 | 低 | 谓词常量表 + 字符串拼接；覆盖 nil/空表/特殊字符转义 |

> L11 行（`entc/gen/graph_test.go:449`、`entc/gen/type_test.go:252`、`schema/field/field_test.go:941`、`dialect/sql/sql_test.go:51` 等）不是库内对编译器优化的直接断言，而是从"生成代码形态 + 生成器决策"提炼的可验证语义（常量表、特性裁剪、分支消除），重写时用行为断言间接验证优化是否正确。

## 推荐收录用例（按优先级）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P1 | `l10_gen_feature_flags_fileset` | entc/gen/graph_test.go:449 | L10/L11 | text/template 生成 6 类文件；feature 开/关两种配置，断言输出文件集合与内嵌常量（1<<32 表） |
| 2 | P0 | `l10_gen_hook_structtag` | entc/gen/graph_test.go:565 | L10/L06 | generator 中间件链 + reflect.StructTag 校验；缺 tag 时整次生成失败 |
| 3 | P0 | `l06_dependency_kind_infer` | entc/gen/graph_test.go:588 | L06/L10 | reflect.Kind + 类型字符串推导字段名，覆盖 slice/map/ptr/后端指针 |
| 4 | P1 | `l10_custom_template_inject` | entc/integration/template/template_test.go:24 | L10 | 自定义模板片段注入生成 struct；生成后用 go/parser 验证字段存在 |
| 5 | P0 | `l10_ident_const_tables` | entc/gen/type_test.go:252,299 | L10/L11 | snake→Camel 常量表 + 关键字/首字母冲突加 `_`；生成代码交 go/parser 复验 |
| 6 | P1 | `l12_globalid_registry_conflict` | entc/gen/globalid_test.go:72 | L12/L10 | table→offset 注册表；冲突标记解析、远端优先、新表取下一个 1<<32 |
| 7 | P0 | `l01_generic_must_helper` | entc/gen/globalid_test.go:116 | L01 | `must[T]` 实例化多类型；值/错误两路径，panic 捕获断言 |
| 8 | P1 | `l01_generic_interceptor_constraint` | entc/integration/hooks/hooks_test.go:724 | L01/L06 | `Limiter[Q interface{ Limit(int) }]`；两个实现类型验证约束与方法集派发 |
| 9 | P0 | `l06_entql_predicate_tree` | entql/types_test.go:14 | L06/L16 | Predicate 接口 + And/Or/Not 组合；递归渲染字符串，覆盖嵌套与否定 |
| 10 | P0 | `l06_entql_expr_string` | entql/entql_test.go:16 | L06/L16 | 表达式 AST 渲染；`has_edge`、`in`、`not in`、nil、浮点常量 |
| 11 | P0 | `l06_rtype_implements` | schema/field/field_test.go:915 | L06 | 值/指针接收者 × 组合接口的 Implements 矩阵；半实现类型必须 false |
| 12 | P0 | `l06_reflect_scan_struct` | dialect/sql/scan_test.go:18 | L06/L13 | 反射扫描：tag 优先、列名归一、`COUNT(*)`、`[]*struct` |
| 13 | P1 | `l06_reflect_scan_nested_ptr` | dialect/sql/scan_test.go:219 | L06 | 内嵌 struct 提升 + `**T`/`*T` 分配链；nil 列留零值 |
| 14 | P1 | `l06_graphson_typed_nil` | dialect/gremlin/encoding/graphson/marshaler_test.go:20,62 | L06/L07 | 自定义 Marshaler/Unmarshaler + `reflect.Value.IsNil` 判 typed-nil；坏输入错误不 panic |
| 15 | P0 | `l10_printer_graph_table` | cmd/internal/printer/printer_test.go:17 | L10 | 自写定宽 ASCII 表；字段/边两表，逐字节 golden 比较 |

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| SQLite/MySQL/Postgres/MariaDB 集成 | entc/integration/integration_test.go:59-108、relation_test.go、json/json_test.go、hooks/hooks_test.go、template/template_test.go | 需要真实数据库；sqlite 走 `github.com/mattn/go-sqlite3`（cgo） |
| Gremlin 服务端/WebSocket | dialect/gremlin/client_test.go、http_test.go、entc/integration/gremlin/gremlin_test.go | 需要外部 Gremlin 服务/网络；`internal/ws` 依赖 WebSocket 运行时 |
| go/packages 加载 schema | cmd/internal/base/packages_test.go:15、entc/load/load_test.go:15 | 依赖 `golang.org/x/tools/go/packages` 与 testdata 目录；partial clone 缺 schema 包 |
| 快照恢复 + go generate | entc/internal/snapshot_test.go:20 | `os/exec` 调 `go generate`，依赖完整生成代码与工具链 |
| CLI 端到端 | cmd/ent/ent_test.go:16、cmd/entc/entc_test.go:16、cmd/entfix/entfix_test.go:21 | 执行命令并依赖 integration 生成物 |
| 第三方断言/Mock | 全仓 `stretchr/testify`、`DATA-DOG/go-sqlmock`、`json-iterator/go`、`modern-go/reflect2`、`google/uuid` | 用例必须单包、仅 stdlib |
| 生成物缺失 | `entc/integration/ent`、`examples/*/ent`、`entc/load/testdata` | partial clone 未取非测试生成代码，相关测试无法编译 |
| 文档格式化/网络示例 | dialect/gremlin/example_test.go、examples/*/example_test.go | 依赖外部服务或示例工程完整代码 |
