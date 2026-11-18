# sqlc v1.28.0

- 来源：`research/repos/sqlc`（partial clone，仅测试文件与 testdata 生成物；commit 349137bd，tag v1.28.0，module `github.com/sqlc-dev/sqlc`，go.mod `go 1.22.9`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/sqlc`
- 重点维度：L10 代码生成（文本 golden）、L11 编译期优化（生成代码上的常量/裁剪语义）、L16 模板/文本解析、L06 接口，以及从生成代码形态提炼的 L01 泛型
- 说明：sqlc 本体没有类型参数；其测试价值在于"SQL/配置 → 生成源码文本"的确定性管线，以及 testdata 中大量 `*.sql.go` 生成物形态（const SQL、Params/Row struct、Scan 循环、枚举 switch、Batch/Querier 接口）。用例用 `text/template` + `go/parser`/`go/types` 复刻生成与校验，不复制生成物。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| internal/endtoend/endtoend_test.go:37 | 遍历 `examples/` 逐个执行 generate，并与目录内 golden 文件逐文件比较 | L10/L11 | 高 | 自写"输入目录 + 模板 + golden 目录"驱动；只保留纯文本产物，失败打印差异 |
| internal/endtoend/endtoend_test.go:107 | `TestReplay`：按 testdata 中 `sqlc.json/yaml` 批量生成，支持 base/managed-db 等上下文与 stderr golden | L10/L11 | 高 | 自建 replay 框架：发现配置 → 生成 → 归一化换行/路径 → golden 比较 |
| internal/endtoend/endtoend_test.go:73 | `BenchmarkExamples`：对每个示例重复 generate，衡量模板渲染吞吐 | L10/L11 | 中 | 固定 schema/query 预解析，循环渲染 N 次；用输出哈希防退化 |
| internal/endtoend/case_test.go:73 | `FindTests`：递归发现 `sqlc.json/yaml/yml`，读取 `stderr.txt`/`exec.json` 形成用例表 | L10 | 中 | filepath.WalkDir + 后缀匹配 + JSON 反序列化；用例排序确定 |
| internal/codegen/golang/result_test.go:10 | 命令类型（exec/execrows/one/many/copyfrom/batch*）→ 是否输出列的类型表 | L11/L10 | 低 | 常量表 + switch 查表；覆盖全部命令与"有列/无列"边界 |
| internal/codegen/golang/result_test.go:70 | 有列时 any 查询强制输出列 | L11 | 低 | 布尔决策函数 + 非空列断言 |
| internal/codegen/golang/opts/override_test.go:9 | 类型覆盖规范解析：`import/path.Type` vs 基础类型，推导 import path/type name/basic | L10/L16 | 中 | 手写 spec 解析器：按最后一个 `.` 拆包路径；对照 basic 类型集合 |
| internal/codegen/golang/opts/override_test.go:103 | Fuzz 解析任意 override spec，不得 panic | L10/L16 | 中 | table 种子 + 随机字符串；解析函数只返回 error |
| internal/source/mutate_test.go:14 | 单处源码编辑：替换/插入/删除，位置+旧串校验；同时生成反向编辑验证可撤销 | L10/L16 | 低 | 字节偏移编辑引擎；正反编辑互逆，含串首尾边界 |
| internal/source/mutate_test.go:89 | 多处编辑：相邻、重叠、长度变化；编辑顺序不影响结果 | L10 | 中 | 编辑按位置排序合并；相邻不重叠可共存，重叠报错 |
| internal/source/mutate_test.go:148 | 单编辑非法输入（位置越界/旧串不匹配）返回错误 | L10 | 低 | 边界检查：loc<0、loc+len(old)>len(src)、old 不匹配 |
| internal/source/mutate_test.go:179 | 多编辑含非法编辑时整体失败，不产生半成品 | L10 | 中 | 先全量校验后统一应用；失败返回原串/错误 |
| internal/sql/named/param_test.go:5 | Param 空值性合并：unspecified/inferred/user-defined 的对称合并表 | L06/L11 | 低 | 三态枚举 + 交换律测试；merge(a,b) 与 merge(b,a) 结果一致 |
| internal/sql/named/param_test.go:52 | Param 名称合并：优先第一个非空名 | L06 | 低 | 名称优先级表，覆盖空白名 |
| internal/metadata/meta_test.go:7 | 解析 `-- name: Foo :one`：三种注释语法、非法名/命令/多余内容 | L16 | 低 | 手写行首注释扫描器；严格 token 数与命令白名单 |
| internal/metadata/meta_test.go:56 | 解析 `@param name TYPE` 行，非法条目忽略 | L16 | 低 | 逐行前缀识别 + 空白切分；未知标记不报错 |
| internal/metadata/meta_test.go:103 | 解析 `@flag-*` 查询标志，支持同一行多个 | L16 | 低 | 前缀扫描 + 去重集合 |
| internal/metadata/meta_test.go:143 | `@rule`/skiplist 类元数据解析 | L16 | 中 | 逗号分隔规则表 + 通配匹配 |
| internal/migrations/migrations_test.go:59 | 去除 goose/sql-migrate/tern/dbmate 的 rollback 段，保留 Up 段原文 | L16 | 低 | 多方言分隔符表；按标记切段并拼接，保留前后空白 |
| internal/migrations/migrations_test.go:74 | golang-migrate 文件名 `.down.sql` 识别 | L16 | 低 | strings.HasSuffix 规则 + 文件名表 |
| internal/config/config_test.go:26 | 配置严格解码：缺 version/packages、未知版本、未知字段均报错 | L06/L16 | 中 | encoding/json + DisallowUnknownFields；错误文本含行号需自建包装 |
| internal/config/config_test.go:72 | Fuzz 配置解码，任意输入不 panic | L16 | 中 | 种子 + 随机文本；只断言不 panic |
| internal/endtoend/vet_test.go:31 | 对所有示例生成代码执行 vet，生成物必须通过静态检查 | L10/L11 | 高 | 生成后 go/parser + go/types 检查（未使用 import、类型不匹配、方法集） |
| internal/endtoend/fmt_test.go:16 | SQL 语句格式化后再指纹化，结果与原指纹一致（幂等） | L16 | 高 | 手写 token 化 + 规范化空白/大小写；比较规范化串而非 AST |
| internal/endtoend/json_schema_test.go:33 | 所有 `sqlc.json` 通过 v1/v2 JSON Schema 校验 | L16 | 中 | 自写最小 schema 校验器或直接结构体严格解码；不引三方 schema 库 |
| internal/endtoend/ddl_test.go:13 | 校验 testdata 中 schema DDL 可被解析 | L16 | 高 | 手写 CREATE TABLE 子集解析器，断言表/列/类型 |
| internal/engine/postgresql/rewrite_test.go:13 | SQL AST 重写：`sqlc.arg(name)` → `$1`，保留 Location | L16/L10 | 高 | 手写 AST + 游标替换；比较重写前后打印结果 |
| internal/engine/sqlite/catalog_test.go:15 | 解析/更新 catalog（表、列、视图） | L16 | 中 | 内存 catalog struct + CREATE/ALTER 子集解析 |
| internal/codegen/sdk/utils_test.go:7 | `LowerTitle`：仅降首 rune，覆盖空串/全大写/标题串 | L11/L16 | 低 | unicode 解码首 rune + unicode.ToLower；逐例断言 |
| internal/shfmt/shfmt_test.go:5 | shell 片段缩进替换 | L16 | 低 | 行前缀替换；无需 shell 解析器 |
| internal/sql/sqlpath/read_test.go:13 | 收集 SQL 文件：忽略隐藏文件、非 .sql、`.down.sql`，支持 glob/符号链接 | L16/L10 | 低 | filepath.Glob + WalkDir + 后缀过滤；排序确定 |

> L01 在 sqlc 库内没有类型参数代码。生成物中的重复形态可提炼为泛型用例：`QueryAll[T]`（对 `internal/endtoend/testdata/emit_enum_valid_and_values/go/query.sql.go:52` 的 rows.Scan 循环）、`BatchResults[T]`（`internal/endtoend/testdata/batch/postgresql/pgx/v5/go/batch.go:26`）、`Null[T]`（`internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:32`）、`AllValues[T]`（同文件 :65）。这些行位于 testdata 生成物，场景来源统一记 `internal/endtoend/endtoend_test.go:107`。

## 推荐收录用例（按优先级）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P0 | `l16_parse_query_annotation` | internal/metadata/meta_test.go:7 | L16 | 严格注释文法：3 种注释前缀 × 合法/非法命令；错误定位到 token |
| 2 | P0 | `l16_parse_query_params_flags` | internal/metadata/meta_test.go:56,103 | L16 | `@param`/`@flag-` 行解析；非法项忽略、重复项去重 |
| 3 | P0 | `l16_strip_migration_rollback` | internal/migrations/migrations_test.go:59 | L16 | 4 种迁移格式分隔符；只保留 Up 原文，逐字节 golden |
| 4 | P1 | `l16_down_migration_filename` | internal/migrations/migrations_test.go:74 | L16 | `.down.sql`/普通 `.sql`/非迁移目录表驱动 |
| 5 | P0 | `l10_source_edit_single` | internal/source/mutate_test.go:14 | L10 | 偏移编辑 + 反向编辑互逆；覆盖串首/串尾/空串 |
| 6 | P1 | `l10_source_edit_multi` | internal/source/mutate_test.go:89,148,179 | L10 | 排序合并、相邻相容、重叠拒绝、失败不落半成品 |
| 7 | P0 | `l11_cmd_dispatch_table` | internal/codegen/golang/result_test.go:10 | L11/L10 | 命令常量表 → 列输出布尔；全命令覆盖 + 有无列边界 |
| 8 | P1 | `l10_type_override_parse` | internal/codegen/golang/opts/override_test.go:9 | L10/L16 | 拆 import path/type；basic 类型白名单；错误路径断言 |
| 9 | P1 | `l06_param_merge_symmetric` | internal/sql/named/param_test.go:5 | L06/L11 | 三态空值性 + 名称优先；验证 merge 对称性 |
| 10 | P1 | `l16_strict_config_decode` | internal/config/config_test.go:26 | L16/L06 | 严格 JSON 解码：未知字段/缺字段/未知版本三类错误 |
| 11 | P1 | `l10_golden_replay_codegen` | internal/endtoend/endtoend_test.go:107 | L10/L11 | 输入（schema+query+config）→ 生成文本 → 归一化 → golden；覆盖 params/Row/const SQL |
| 12 | P1 | `l10_generated_enum_switch` | internal/endtoend/endtoend_test.go:107 + `internal/endtoend/testdata/emit_enum_valid_and_values/go/models.go:12-65` | L10/L11 | 枚举：String 常量表、Scan 类型 switch、Valid/AllValues；用同形态手写代码生成并验证 |
| 13 | P1 | `l06_generated_interfaces` | internal/endtoend/endtoend_test.go:107 + `internal/endtoend/testdata/alias/postgresql/pgx/v5/go/db.go:14`、`internal/endtoend/testdata/copyfrom/postgresql/pgx/v5/go/querier.go:13` | L06/L10 | DBTX/Querier 接口 + `var _ Querier = (*Queries)(nil)` 方法集断言；用假实现调用 |
| 14 | P1 | `l11_dce_unused_structs` | internal/endtoend/endtoend_test.go:107 + `internal/endtoend/testdata/omit_unused_structs/postgresql/stdlib/sqlc.json` | L11/L10 | 仅生成被查询引用的模型 struct；断言未引用表 struct 不存在（生成期 DCE 语义） |
| 15 | P0 | `l01_generic_scan_helpers` | internal/endtoend/endtoend_test.go:107 + `internal/endtoend/testdata/emit_enum_valid_and_values/go/query.sql.go:52`、`internal/endtoend/testdata/batch/postgresql/pgx/v5/go/batch.go:26` | L01/L06 | 从生成循环提炼 `QueryAll[T]`/`QueryOne[T]`/`BatchResults[T]`/`Null[T]`，多类型实例化 + 方法集约束 |

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| 真实数据库示例 | examples/authors|booktest|ondeck 的 `db_test.go`、endtoend 的 `managed-db` 上下文、`vet_test.go` | 需要 MySQL/Postgres/SQLite 实例与驱动 |
| cgo SQL 解析 | internal/endtoend/fmt_test.go:16、internal/engine/postgresql/rewrite_test.go:13、internal/endtoend/ddl_test.go:13 | `pg_query_go` 经 cgo 链接 libpg_query |
| 插件运行时 | testdata/process_plugin_*（exec.json）、testdata/wasm_plugin_* | 需要外部进程协议或 wazero WASM 运行时 |
| 第三方库 | gojsonschema（json_schema_test）、yaml.v3（config）、shfmt、go-cmp | 用例禁止三方依赖 |
| 完整代码生成 CLI | TestExamples/TestReplay/BenchmarkExamples | 依赖 `internal/cmd`、plugin proto、非测试源码与完整 examples；partial clone 缺失 |
| 生成物直接编译 | testdata 中 `query.sql.go` 导入 pgx/pgtype/stdlib | 生成物依赖第三方驱动类型，不能单包编译 |
| Fuzz 用例 | internal/config/config_test.go:72、internal/codegen/golang/opts/override_test.go:103 | 需 Go fuzz 引擎与语料，非确定性 |
| 数据库 catalog/DDL 全量解析 | internal/engine/sqlite/catalog_test.go:15、internal/endtoend/ddl_test.go:13 | modernc sqlite/完整 SQL 方言，超出 stdlib 手写子集 |
