# gorm v1.30.0

- 来源：`research/repos/gorm`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / `README`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/gorm`
- 本地核验：`git describe` = `v1.30.0`，HEAD `c44405a2`（2025-05-25，`Implement Generics API (#7424)`）
- 模块：`gorm.io/gorm`，`go 1.18`
- 范围：仓库内 86 个 `*_test.go`。按 `reflect|schema|sync|atomic|go func|chan|clone|Map|Slice|tag|generic` 筛选后，精读 `schema/**`、`statement_test.go`、`callbacks/**`、`clause/expression_test.go`、`tests/{lru,prepared_stmt,generics,preload,associations_many2many,sql_builder,scanner_valuer}` 等约 22 个文件；文件:行均相对本仓根目录。
- 说明：`tests/` 包在 `init()` 中连接真实数据库（默认 sqlite 文件，另支持 MySQL/PostgreSQL/SQLServer/TiDB），因此本库的 L06 主战场是**不依赖 DB 的 `schema/` 反射解析与 `statement`/`clause` 纯逻辑**；涉及 SQL 执行的并发场景只提炼机制，用例一律以 stdlib 数据结构重写。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| `schema/schema_test.go:13` | `schema.Parse(&User{}, &sync.Map{}, NamingStrategy{})`：反射遍历 10 个字段与 8 个关系（has one/has many/belongs to/many2many），解析 `gorm` tag、主键、大小写命名、外键引用 | L06/L13 | 高 | 自写 `parseSchema(reflect.Type)`：字段名/类型/tag → `Field`，再扫嵌套 struct/slice 推断关系；缓存 `sync.Map[reflect.Type]*Schema` |
| `schema/schema_test.go:22` | 内嵌 `tests.User` 并追加 `Attrs map[string]string` 字段，`type:Map(String,String)` 覆盖 DataType | L06 | 低 | 反射识别 map 字段；tag 中的 `type:` 覆盖默认类型名 |
| `schema/schema_test.go:38` | 全指针字段模型（`*gorm.Model`、`*string`、`*int`、`*bool`、切片指针），解析结果与非指针版一致 | L06/L17 | 中 | 反射递归解引用 `reflect.Ptr`；记录字段可空性；GC 后再次解析同一类型验证缓存 |
| `schema/schema_test.go:135` | 高级数据类型：`sql.NullInt64`、`*sql.NullString`、`sql.NullTime`、`type mytime time.Time`、`mybool = bool`，校验 DataType | L06 | 中 | `reflect.Type` 的 Named/alias 判定 + `sql.Scanner`/`driver.Valuer` 接口探测；type switch |
| `schema/schema_test.go:181` | 三层嵌套模型 `VersionUser → VersionModel → BaseModel`，字段 BindNames 保留完整路径 | L06/L04 | 中 | 递归遍历匿名嵌入字段；BindNames 为前缀链；深度递归可放大到 10k 层测栈 |
| `schema/schema_test.go:202` | 嵌入 struct + `gorm:"embedded;embeddedPrefix:company_"`，字段 DB 名加前缀；`gorm:"-"` 忽略；内嵌主键与外层主键并存 | L06/L13 | 高 | 递归展开 embedded 字段，前缀拼接 DBName；ignored tag 过滤；主键冲突处理 |
| `schema/schema_test.go:270` | 自定义 NamingStrategy 下 embedded 字段命名，验证 replacer/表前缀/单数表名 | L06 | 中 | 可替换命名策略接口；`strings.NewReplacer` + 大小写转换 |
| `schema/schema_test.go:313` | 复合主键 + 自增：`Product` 的 `ProductID` 自增、`LanguageCode` 参与复合主键；另一模型显式 `autoIncrement:false` | L06/L13 | 中 | 主键字段集合 + autoIncrement 标记；按 tag 顺序决定自增列 |
| `schema/field_test.go:16` | `Field.Set(ctx, reflectValue, v)` 对 `string/int/time.Time/bool` 及 `sql.NullString`、`*sql.NullInt64`、alias 类型赋值；`ValueOf` 反向取值 | L06 | 高 | `reflect.Value.FieldByName` + `Convert`/`Set`；对 Scanner/Valuer 接口做适配；表驱动覆盖 nil/指针/别名 |
| `schema/field_test.go:92` | 指针字段 Setter：`*string/*uint/*bool/*time.Time` 与 nil 清零语义 | L06 | 中 | 反射分配新值并 `Set`；nil 输入需区分“无值”和“零值” |
| `schema/field_test.go:162` | 高级类型混合 Setter/Getter（`sql.Null*`、命名时间类型、`*mybool`） | L06 | 中 | 接口断言 + `reflect.New`；类型转换失败返回确定错误 |
| `schema/field_test.go:242` | 字段权限 tag：`<-:create`、`->:false`、`-:migration` 等组合决定 Creatable/Updatable/Readable | L06/L13 | 中 | 解析 tag 字符串为权限位；反射字段写入前检查权限 |
| `schema/field_test.go:305` | `type TypeAlias = ...` 与命名别名类型的字段解析/赋值差异 | L06 | 中 | `reflect.Type.Name()`/`Kind()` 区分 alias 与 defined type |
| `schema/relationship_test.go:96` | 自引用 belongs-to：`Manager *User` + `ManagerID`，外键指向自身表 | L06 | 中 | 关系图 `map[string]*Relation`；检测自引用并复用同一 Schema |
| `schema/relationship_test.go:483` | 自引用 many2many：`Friends []*User` 生成 join 表 `user_friends`，两端字段均为 UserID/FriendID | L06/L13 | 高 | 关系推断 + join 表去重；双向引用合并为同一 join schema |
| `schema/relationship_test.go:603` | 多态关系：`Toy` 的 OwnerID/OwnerType 指向 Cat/User；自定义 `polymorphicType/polymorphicId`、仅 Type、嵌入多态等 6 组子用例 | L06/L04 | 高 | 按 tag 解析多态字段对；类型值由所属 Schema 推导；递归解析 EmbeddedRelations |
| `schema/relationship_test.go:518` | 嵌入关系 `EmbeddedRelations` 与 `TestEmbeddedHas`（`:544`），关系随嵌入 struct 展开 | L06 | 中 | 递归展开 embedded 字段并合并关系表；前缀命名 |
| `schema/relationship_test.go:858` | 匿名嵌入 `var result struct{ User }` 的“变量关系”：Schema 名为空，外键仍从嵌入类型推断 | L06 | 中 | 反射匿名 struct 字段，允许父 Schema 名为空；关系只在字段级解析 |
| `schema/index_test.go:58` | `gorm:"index:...,unique,sort,collate,length,where,class,expression,priority"` 全量解析；嵌套/复合索引（`composite:comp_id1`）跨 3 层嵌入合并 | L06/L13 | 高 | 解析 `index:` 选项 map；按 name 分组并按 priority 排序；递归嵌入收集 composite 字段 |
| `schema/constraint_test.go:18` | `check:` 约束命名与表达式（显式名/自动 `chk_<table>_<field>`）；`:58` unique/uniqueIndex 约束 | L06/L13 | 中 | tag 解析 + 默认命名规则；constraint map 去重 |
| `schema/callbacks_test.go:22` | 回调方法集探测：`BeforeSave`/`AfterCreate` 等通过反射在类型方法集中存在则置位；未实现者为 false | L06 | 中 | `reflect.Type.MethodByName` 扫一组回调名；方法集含指针接收者规则 |
| `schema/naming_test.go:8` | `toDBName`/`SchemaName` 大小写与缩写转换（`HTTPServerHandlerForURLID`→`http_server_handler_for_url_id`）；`:192` 63/64 字符标识符截断加 hash | L13/L05 | 中 | 手写下划线/驼峰状态机；超长名截断 + 短 hash；表驱动 |
| `statement_test.go:11` | `Statement.clone()` 逐层复制 WHERE 条件：1–8 层 clone 后分别追加 FINAL1/FINAL2，两份条件不得互相污染 | L13/L06 | 中 | 深拷贝 `map[string]clause` + 条件切片；验证 slice/map 独立；可加 GC 触发旧语句回收 |
| `statement_test.go:45` | `matchName` 解析带引号/反引号/点号的表列名（`"table"."name"`、`my_table.*`、非法 `"table"."*"`） | L13 | 低 | 手写引号/分隔符状态机；表驱动 18 组 |
| `callbacks/helper_test.go:11` | `loadOrStoreVisitMap` 以 `reflect.Value` 为 key 记录已访问节点：单值、重复值、切片批量，返回是否已存在 | L06/L13 | 中 | 以指针/`reflect.Value.Pointer()` 为 key 的 visit set；切片递归展开 |
| `callbacks/helper_test.go:41` | `ConvertMapToValuesForCreate` / `:99` slice-of-map → `clause.Values`，按 map 键排序生成列与行 | L13/L06 | 中 | map→有序列 + 行矩阵；`sort.Strings` 保证确定性；`any` 值直传 |
| `callbacks/create_test.go:16` | 反射判断 Dest 是 slice/array/struct，构造 `clause.Values` 的列与行（含空 slice、指针 slice） | L06/L05 | 中 | `reflect.Kind` 分支 + 元素遍历；预分配 `[][]any` |
| `clause/expression_test.go:16` | `clause.Expr.Build` 把 `?` 占位符替换为 `Table/Column/Expr` 的 SQL 片段（带引号）；`:39` 命名参数 `@name`/`sql.Named`/struct/map 展开 | L13/L06 | 中 | 手写占位符扫描器 + `reflect` 读取 struct 字段（含嵌入）；参数展开为 vars |
| `tests/lru_test.go:350` | 1000 goroutine 并发 `Add` 100 个 key，最终 `Len()==100`；另有 TTL 过期（`:262`/`:397`）与 Remove 回调 | L02/L13 | 中 | 泛型 LRU：`map[K]*list.Element` + `container/list` + `sync.Mutex`；TTL 用时间戳，并发 Add/Get/Remove |
| `tests/prepared_stmt_test.go:153` | `MaxOpenConns(1)` 下 100 goroutine 并发 Create/First，PrepareStmt 缓存必须去重为 2 条语句且不死锁；`:237` 读循环与 `Close()` 并发 | L02/L13 | 高 | `sync.Map`/LRU 语句缓存 + 单连接信号量；100 goroutine 争用，Close 与查询竞态 |
| `tests/generics_test.go:772` | 泛型查询链 `reusedb := gorm.G[User](DB).Where(...)` 被 5 个 goroutine 复用，各自追加 Where/Find 不得互相污染 | L02/L06/L11 | 高 | 泛型 API 返回不可变链：每次 Where 复制 statement（slice/map 深拷贝），5 goroutine 并发执行 |
| `tests/preload_test.go:239` | 10 个 goroutine 各自 `Session(&gorm.Session{})` + Preload 查询同一用户，验证 Session 隔离 | L02/L06 | 中 | 每次调用复制查询上下文；并发执行，校验结果一致且无共享 map 写 |
| `tests/associations_many2many_test.go:360` | 3 个 goroutine 并发 `Association("Languages").Append`，之后 Preload 校验关联数为 3 | L02/L13 | 中 | 关联集合 `map[key]struct{}` + 锁；并发 Append 去重，GC 后计数 |
| `tests/sql_builder_test.go:155` | DryRun 生成 SQL 与 Vars：Create 9 个参数、Find 1 个参数；不执行 DB | L06/L11 | 低 | 自写 SQL 构造器 + 参数切片；DryRun 只构建不执行，比较 SQL/args |
| `tests/generics_test.go:21` | `gorm.G[User](DB)` 泛型 API：Create/First/Take/Scan/MapColumns/批处理/事务，覆盖类型参数推导 | L01/L06 | 高 | 用 stdlib 泛型 `Repo[T]` 模拟链式查询；DB 部分改为内存表 |
| `tests/scanner_valuer_test.go:21` | 结构体字段实现 `sql.Scanner`/`driver.Valuer`（`sql.Null*`、自定义 `EncryptedData`、`StringsSlice`、struct slice），读写往返 | L06 | 高 | 定义 Scanner/Valuer 接口 + 内存编解码；反射选择接口路径 vs 基础类型路径 |
| `utils/utils_test.go:114` | `ToString` 对 int/uint 各宽度/string/bool 的 type switch；`:44` `ToStringKey` 对切片参数展开 | L06/L13 | 低 | type switch + `strconv`；嵌套 slice 用递归/`fmt.Sprint` 语义 |
| `tests/transaction_test.go:301` | 深层嵌套事务 + 包装回调：Savepoint 逐层回滚/提交，回调 panic 时外层回滚 | L02/L19 | 高 | 事务栈 + savepoint 名栈；panic/recover 保证回滚；可加 goroutine 并发事务 |

## 推荐收录（15 条）

1. `l06_schema_parse_relations` — `schema/schema_test.go:13`。反射解析 10 字段 + 8 个关系；P2，L06/L13。默认缩为 3 类关系，`TORTURE_MODEL` 放大字段数。
2. `l06_schema_embedded_prefix` — `schema/schema_test.go:202`。embedded + prefix + ignored + 双主键；P1，L06/L13。
3. `l06_schema_pointer_fields` — `schema/schema_test.go:38`。指针/双指针/切片指针字段解析与 GC 后复用；P1，L06/L17。
4. `l06_field_reflect_setter` — `schema/field_test.go:16`。`reflect.Value.Set/Convert` + Scanner/Valuer + 别名类型；P1，L06。
5. `l06_field_pointer_setter` — `schema/field_test.go:92`。指针字段赋值与 nil 语义；P1，L06。
6. `l06_polymorphic_relations` — `schema/relationship_test.go:603`。多态 OwnerID/OwnerType + 自定义命名 + 嵌入多态；P2，L06/L04。缩为 3 组子用例。
7. `l06_self_ref_many2many` — `schema/relationship_test.go:483`。自引用 many2many join 表生成；P1，L06/L13。
8. `l06_variable_relation` — `schema/relationship_test.go:858`。匿名嵌入 struct 的变量关系解析；P1，L06。
9. `l06_index_tag_parse` — `schema/index_test.go:58`。index 全选项 + 嵌套 composite 索引；P1，L06/L13。
10. `l06_callback_methodset` — `schema/callbacks_test.go:22`。反射方法集探测生命周期回调；P0，L06。适合验证指针/值接收者方法集规则。
11. `l02_lru_concurrent` — `tests/lru_test.go:350`。1000 goroutine 并发 LRU Add + TTL 过期；P1，L02/L13。std lib `container/list` + mutex，GC 穿插。
12. `l02_stmt_cache_deadlock` — `tests/prepared_stmt_test.go:153`。单连接 + 100 goroutine + 语句缓存去重；P1，L02/L13。`MaxOpenConns(1)` 用带缓冲信号量模拟。
13. `l02_generic_chain_reuse` — `tests/generics_test.go:772`。5 goroutine 复用同一条泛型查询链，Where 不得互相污染；P1，L02/L06/L11。对应 `Statement.clone` 深拷贝。
14. `l13_where_clone` — `statement_test.go:11`。1–8 层 WHERE 条件 clone 后互不污染；P1，L13/L06。深拷贝 map/slice，GC 后校验旧副本。
15. `l04_schema_deep_embed` — `schema/schema_test.go:181`（合成放大）。`VersionUser→VersionModel→BaseModel` 的三层嵌入放大到 10k 层匿名 struct，递归解析字段与 BindNames；P1，L04/L06。以深度触发 morestack/栈复制，并设最大深度错误路径。

## 不可移植清单

- **真实数据库**：`tests/**` 的绝大部分文件（`query_test.go`、`create_test.go`、`update_test.go`、`delete_test.go`、`migrate_test.go`、`preload_*`、`associations_*`、`joins_*`、`transaction_test.go`、`count/group_by/distinct/upsert/soft_delete/scan/table/hooks/callbacks`）。`tests/tests_test.go` 的 `init()` 会连接 sqlite/MySQL/PostgreSQL/SQLServer/TiDB 并执行 `AutoMigrate`，无 DB 直接 `os.Exit(1)`；SQL 方言、DDL、锁语义无法用 stdlib 等价。
- **方言专属**：`tests/postgres_test.go`、`tests/non_std_test.go`、`tests/table_test.go:178`（PostgreSQL 标识符长度）、`tests/error_translator_test.go`、`tests/connpool_test.go`（MySQL wrapper）。依赖 dialector、真实驱动错误码与连接池统计。
- **预编译语句/连接池**：`tests/prepared_stmt_test.go`、`tests/connpool_test.go`、`tests/transaction_test.go:432`（closed conn）、`:481`（默认超时）。依赖 `database/sql` 与真实 DB 的 stmt/事务生命周期；只能重写缓存/信号量机制。
- **Scanner/Valuer 落库**：`tests/scanner_valuer_test.go`、`tests/serializer_test.go`、`tests/customize_field_test.go`、`tests/embedded_struct_test.go`、`tests/scan_test.go`。接口路径可提炼，但 `driver.Valuer` 的最终赋值依赖 SQL 引擎。
- **泛型 API 执行**：`tests/generics_test.go` 的 `gorm.G[T]` 全部子用例。泛型类型推导可移植，但每个用例都执行 SQL；需改为内存 `Repo[T]`。
- **内部包依赖**：`tests/lru_test.go`（`gorm.io/gorm/internal/lru`）、`schema/**` 的 `gorm.io/gorm/utils/tests` 模型、`clause/**`、`callbacks/**`。`internal` 包禁止外部模块导入，且用例规则不允许三方依赖；场景须按 stdlib 重写。
- **驱动/cgo**：默认 sqlite 驱动在部分配置下需要 cgo（`mattn/go-sqlite3`）与临时文件 `os.TempDir()/gorm.db`；CoreGoLLVM 独立编译环境不可用。
- **平台文件**：`utils/utils_unix_test.go`、`utils/utils_windows_test.go` 依赖 `//go:build` 与 OS 路径/权限语义。
- **Benchmark/规模**：`tests/benchmark_test.go`、`clause/benchmarks_test.go`、`tests/migrate_test.go` 的部分 2000+ 行压测。依赖 DB 吞吐，属于基准而非确定性稳定性用例。
- **日志/回调链**：`tests/tracer_test.go`、`tests/callbacks_test.go`、`tests/hooks_test.go`。依赖 DB 生命周期与 logger/tracer 注入点；可提炼“回调顺序”纯逻辑，但原代码不能独立运行。
