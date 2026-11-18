# protobuf-go v1.36.6

- 来源：`research/repos/protobuf-go`（partial clone，仅测试文件与 testdata；commit 3f79c52e，tag v1.36.6，module `google.golang.org/protobuf`，go.mod `go 1.22`）
- 本地路径：`/mnt/d/work/Compiler/CoreGoLLVM/CoreGoVLLM-test/research/repos/protobuf-go`
- 重点维度：L06 反射/接口、L10 代码生成（protogen 文本管线与生成代码形态）、L11 编译期优化语义、L07/L18 unsafe 与 GC、L12 描述符初始化
- 说明：protobuf-go 本体没有类型参数（L01 需从 `internal/set`、`internal/order`、`testing/prototest` 的形态提炼）。测试大量依赖 `internal/testprotos` 的 `.pb.go` 生成物与 protoc，partial clone 中不可编译；可移植部分集中在 `protoreflect.Value`、protowire、tag、strs、defval、order、set、protogen 文本构建等纯 Go 逻辑。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| reflect/protoreflect/value_test.go:20 | `Value` 联合体：nil/bool/int32/int64/uint32/uint64/float32/float64/string/[]byte/Message/List/Map 共 13 种 kind 的存取与 type switch | L06/L13 | 中 | 自建 `Value struct{kind; i; f; s; b; ptr}` + 每 kind 存取方法；覆盖每类边界值 |
| reflect/protoreflect/value_test.go:104 | `ValueEqual`：NaN 相等、Inf、nil/空 bytes、不同 kind 不相等 | L06/L11 | 低 | 按 kind 分支比较；浮点用 `math.IsNaN`，bytes 用 nil/len 语义 |
| reflect/protoreflect/value_test.go:145 | ValueOf 内联路径 vs 接口装箱的分配基准 | L11 | 中 | 包级 Value 变量 + `testing.AllocsPerRun` 对比具体类型赋值；不打印时间 |
| proto/methods_test.go:45 | Legacy `Marshal() ([]byte,error)` 快速路径：经 `impl.Export.MessageOf` 包装后调用用户方法，Size/MarshalAppend/Deterministic 一致 | L06/L10 | 中 | 定义带 Marshal/Unmarshal 方法的类型，接口断言优先走自有方法，否则反射回退 |
| proto/methods_test.go:75 | Legacy Unmarshal 方法调用与错误传播 | L06 | 低 | 接口方法存在性检测 + 调用；错误原样返回 |
| proto/methods_test.go:99 | `Descriptor()` panic 不影响 Marshal 走 Marshal 快速路径 | L06 | 低 | recover 隔离：仅在无自有 Marshal 时才触碰 Descriptor |
| proto/methods_test.go:135 | 解码后 `UnmarshalInitialized` 标志与 `partial` 期望一致 | L11/L06 | 中 | 位标志状态机：解码过程中记录 required 字段缺失，输出 flags |
| proto/methods_test.go:172 | Legacy `Merge(src MessageV1)` 方法被优先调用，src 不被修改 | L06 | 中 | 接口检测 + 深拷贝合并；断言源对象不变 |
| proto/nil_test.go:20 | nil vs typed-nil 消息传给 Size/Marshal/Unmarshal/Merge/Equal 的 panic/返回矩阵 | L06 | 低 | 表驱动 `recover` 断言；区分 `any==nil` 与 reflect nil |
| proto/oneof_get_test.go:25 | Open/hybrid 生成消息的 oneof Get/Has/Clear/Set 方法集，字段表闭包驱动 | L10/L06 | 中 | 手写生成风格 oneof：接口 + 多个包装 struct + Get/Has 方法，表驱动覆盖 |
| proto/oneof_get_test.go:150 | Opaque 生成消息的 oneof 访问器与零值语义 | L10/L06 | 中 | opaque 形态：未导出字段 + 访问器；零值/未设置/显式零值三态 |
| proto/reset_test.go:15 | 生成消息 `Reset()` 方法，字段清空后仍可复用 | L10/L06 | 低 | 生成风格 struct + Reset 方法；清空后重复赋值 |
| proto/equal_test.go:23 | Equal 的 nil/typed-nil/相同指针/不可比较类型短路 | L06/L11 | 中 | 反射+类型断言混合：同指针短路、typed-nil 区分、不可比较类型走反射比较 |
| proto/extension_test.go:105 | `HasExtension` 在 lazy 扩展上不触发解码/分配 | L05/L11 | 中 | lazy 容器：只记录原始字节和 presence 位；用 AllocsPerRun 断言零分配 |
| proto/extension_test.go:354 | 并发 GetExtension，首次解码只发生一次 | L02/L18 | 中 | sync.Once + 原子状态；竞争下指针一致性 |
| proto/merge_test.go:776 | 并发 Merge 同一消息的竞态安全 | L02/L06 | 高 | 互斥锁保护合并；-race 语义手写验证（单用例不依赖 race 构建） |
| proto/checkinit_test.go:19 | required 字段递归检查：optional/repeated/map 嵌套缺失的错误路径 | L11/L06 | 中 | 生成风格描述表 + 递归检查；错误消息包含完整字段路径 |
| internal/encoding/tag/tag_test.go:18 | protobuf struct tag 的 Marshal/Unmarshal：`bytes,1337,rep,name=...,json=...,proto3,def=...` | L10/L06 | 中 | 手写 tag 词法（逗号分隔、转义、八进制 bytes）+ reflect.TypeOf 驱动 |
| internal/filedesc/build_test.go:23 | 描述符初始化：filedesc 构建结果与 protoc 生成的 gzip FileDescriptorProto 对齐；字段全覆盖检查 | L12/L10 | 高 | 手写描述符表 + init 注册；用 map[FullName] 标记覆盖，重复注册报错 |
| internal/filedesc/desc_test.go:32 | 由 FileDescriptorProto 构建文件描述符，含默认值/oneof/map 条目 | L10/L12 | 高 | 常量表驱动构造嵌套描述符；确定性遍历并打印 |
| internal/order/order_test.go:50 | 字段排序表：extension 优先、oneof 内/外、synthetic oneof、按 number/index | L11/L10 | 中 | 预计算 order 常量表 + sort 比较函数；两种 order 下的稳定序列 |
| internal/set/ints_test.go:24 | 位图 `Ints`：Set/Has/Clear/Len，1024 位随机置位/清除 | L01/L13 | 低 | 泛型 `Set[T ~uint32|~uint64]` + word 位运算；边界 0/63/64/1023/1024 |
| internal/strs/strings_test.go:12 | `GoCamelCase`：下划线/点/Unicode/连续大写/数字的标识符转换 | L10/L11 | 低 | rune 状态机；golden 表覆盖 ASCII/Unicode/前导下划线 |
| internal/strs/strings_test.go:45 | `GoSanitized`：非法字符→下划线、关键字前缀 `_`、多字节 rune 保留 | L10/L11 | 低 | 逐 rune 白名单；keyword 表前缀 `_` |
| internal/strs/strings_test.go:67 | 名称推导：map entry、enum value、JSON camel/snake、枚举前缀剥离 | L10 | 中 | 复合命名规则表：camel/snake/前缀去重；与 enumprefix 用例联动 |
| internal/encoding/defval/default_test.go:17 | 默认值格式化：bool→true/1、int、float（Pi 精度）、string、bytes 转义 | L11/L10 | 低 | 按 kind 的格式化函数表；含 `%q`/octal 转义与极值 |
| internal/impl/lazy_test.go:26 | lazy 扩展：未访问不解码；Marshal/Size/Has/Get 后状态迁移 | L11/L07 | 高 | lazy 字段容器：raw bytes + 状态位；访问时才解析并缓存 |
| internal/impl/lazy_test.go:500 | MessageSet lazy 并发 Marshal/Get 竞态 | L02/L07 | 高 | 原子状态 + 锁；校验并发访问下扩展值不丢 |
| internal/impl/legacy_test.go:607 | 并发包装 legacy 类型/枚举，5 路 goroutine 必须得到同一描述符指针 | L12/L02 | 中 | sync.Once/registry map + 指针相等断言 |
| internal/impl/message_reflect_test.go:1466 | `Reset()` 不得清空 messageState 缓存（清空会 panic） | L06/L18 | 中 | 结构体内嵌状态指针；Reset 只清业务字段，保留状态对象 |
| internal/impl/message_reflect_test.go:1493 | unsafe 把具体消息视为 MessageState 后，GC 多轮仍保留对象其余内存；反射视图回转具体类型值不损坏 | L07/L17/L18 | 高 | `unsafe.Pointer` 指向更大 struct 后只保存反射视图；runtime.GC + 读取字段校验 |
| internal/impl/lazy_buffersharing_test.go:91 | lazy 消息与输入 buffer 共享语义：不必要拷贝不发生、拷贝后隔离 | L07/L05 | 高 | 引用计数/切片别名；修改输入前后校验 lazy 值 |
| internal/reflection_test/reflection_large_opaque_test.go:23 | 大消息（大量字段）经原型/反射/影子视图读写一致 | L10/L06 | 高 | 生成风格大 struct + 反射读写器；多视图交叉校验 |
| internal/impl/enum_test.go:15 | 枚举类型 `Type().New(n)` 返回值等于枚举值本身 | L06 | 低 | 枚举接口 + New 方法；值/未知值 |
| compiler/protogen/protogen_test.go:91 | 包名/导入路径推导：同 import path 包名一致、冲突检测 | L10 | 中 | import path→包名 map + 一致性校验 |
| compiler/protogen/protogen_test.go:275 | 生成文件 import 去重、同名包加序号别名、预声明标识符（string）避让 | L10/L11 | 中 | 文本生成器：引用收集→排序→别名分配；输出与 golden 一致 |
| compiler/protogen/protogen_test.go:350 | 生成代码注解：符号起止字节偏移映射回 proto path（GeneratedCodeInfo） | L10/L14 | 高 | 生成时记录每个符号的 byte offset；解析生成文本后按偏移切片校验 |
| cmd/protoc-gen-go/enumprefix_test.go:13 | 枚举前缀剥离：默认剥离、Both 同时生成、无前缀/排除项保留原名 | L10 | 低 | 前缀表 + 生成名集合；每个枚举值生成 1 或 2 个常量 |
| cmd/protoc-gen-go/testdata/gen_test.go:10 | 所有 testdata 包以 blank import 注册，验证 init 注册表可加载 | L12 | 低 | 多个小包 + 常量 init 注册；重复注册检测 |
| encoding/protowire/wire_test.go:145 | varint 解析：0/1/5/10 字节边界、截断、超长（>10 字节）拒绝 | L11/L13 | 低 | 手写 varint 解码；每步检查 `len(b)` 并限制 10 字节 |
| encoding/protowire/wire_test.go:652 | ZigZag 编解码：MinInt64/MaxInt64/0/±小值 | L11 | 低 | 位运算 `(n<<1)^(n>>63)` + 边界表 |
| internal/msgfmt/format_test.go:33 | 消息文本格式化：标量/bytes 转义/嵌套/Any/时间戳/包装类型 | L06 | 中 | 反射遍历 + 类型名分派；输出排序确定 |
| proto/size_test.go:12 | 空消息 Size 为 0 的快速判定 | L11 | 低 | 空描述表 + 早退；边界 nil |

> L01 说明：库内无类型参数。上表 L01 行来自可泛型化重写（缓存/集合/测试驱动）；`protoreflect.Value` 不适合泛型化，保留为 kind 联合体更贴近原语义。

## 推荐收录用例（按优先级）

| # | 优先级 | 建议用例名 | 来源 | 维度 | 重写要点 |
|---|---|---|---|---|---|
| 1 | P0 | `l06_value_union_typeswitch` | reflect/protoreflect/value_test.go:20 | L06/L13 | 13 kind 联合体 + 每 kind 边界值；type switch 存取一致 |
| 2 | P0 | `l06_value_equal_float_edge` | reflect/protoreflect/value_test.go:104 | L06/L11 | NaN 相等、Inf、nil/空 bytes、跨 kind 不等 |
| 3 | P0 | `l06_legacy_marshal_fastpath` | proto/methods_test.go:45 | L06 | 接口方法存在则走用户实现，否则回退；错误原样传播 |
| 4 | P0 | `l06_typed_nil_dispatch` | proto/nil_test.go:20 | L06 | nil/typed-nil × 各 API 的 panic/返回矩阵，recover 断言 |
| 5 | P1 | `l10_oneof_method_tables` | proto/oneof_get_test.go:25,150 | L10/L06 | 生成风格 oneof：接口包装 + Get/Has/Clear/Set；表驱动全字段 |
| 6 | P0 | `l10_tag_marshal_grammar` | internal/encoding/tag/tag_test.go:18 | L10/L06 | tag 文法与 reflect.TypeOf；bytes 八进制转义、rep/def/proto3 标志 |
| 7 | P1 | `l12_descriptor_init_registry` | internal/filedesc/build_test.go:23 | L12/L10 | init 注册描述符 + 全字段覆盖校验；重复/缺失注册报错 |
| 8 | P1 | `l11_field_order_tables` | internal/order/order_test.go:50 | L11/L10 | 预计算排序表 + 比较函数；两种 order 稳定序列 |
| 9 | P0 | `l01_generic_bitset_set` | internal/set/ints_test.go:24 | L01/L13 | `Set[T ~uint32|~uint64]`；边界字/位、Set/Has/Clear/Len |
| 10 | P0 | `l10_identifier_naming_tables` | internal/strs/strings_test.go:12,45 | L10/L11 | 编译期常量输入 → Camel/Sanitized 输出；ASCII/Unicode/关键字 |
| 11 | P1 | `l11_default_value_format` | internal/encoding/defval/default_test.go:17 | L11/L10 | 每 kind 的常量格式化；精度/转义/极值 |
| 12 | P1 | `l11_lazy_decode_state` | internal/impl/lazy_test.go:26 | L11/L07 | raw bytes 延迟解码；访问后状态迁移与缓存 |
| 13 | P0 | `l07_unsafe_messagestate_gc` | internal/impl/message_reflect_test.go:1493 | L07/L17/L18 | unsafe 视图持有大 struct；多轮 GC 后字段值不损坏 |
| 14 | P1 | `l13_wire_varint_bounds` | encoding/protowire/wire_test.go:145 | L13/L11 | 1/5/10 字节、截断、超长拒绝；每步长度检查 |
| 15 | P1 | `l10_protogen_import_alias` | compiler/protogen/protogen_test.go:275 | L10 | 引用收集→排序→同名包别名→预声明标识符避让；golden 文本 |

## 不可移植清单

| 场景 | 代表位置 | 原因 |
|---|---|---|
| protoc/protoc-gen-go 生成链 | cmd/protoc-gen-go/testdata/gen_test.go:10、generate-protos、descriptor_test、builder_test、opaque 测试 | 需要 protoc 可执行文件与生成的 `.pb.go`；partial clone 未包含 |
| testprotos 生成消息 | `internal/testprotos/**`（test、test3、testeditions、legacy、lazy、nullable 等） | 测试直接引用生成的 Message/Enum/Extension 类型；生成物缺失 |
| 一致性/兼容性套件 | internal/conformance/conformance_test.go、internal/cmd/pbdump、internal/benchmarks | 需要 conformance runner、benchmark 框架或完整测试协议 |
| go-cmp/protocmp | 绝大多数 `*_test.go` | 用例禁止三方依赖；需改写为手写比较或 golden |
| 构建标签专用路径 | proto/methods_test.go:6（`!protoreflect`）、internal/testprotos/nullable/methods_test.go（`protoreflect`） | 同一用例集按 tag 切换实现，单包用例无法覆盖全部构建矩阵 |
| 随机化输出 | internal/msgfmt/format_test.go:33、internal/filedesc/desc_test.go:32（依赖 `detrand.Disable()`） | 默认输出含随机化，必须显式禁用；用例要求确定性 |
| Fuzz 套件 | encoding/protojson/fuzz_test.go、prototext/fuzz_test.go、internal/fuzz/* | 需要 Go fuzz 引擎与语料 |
| race 专用测试 | internal/race_test/*、proto/merge_test.go:776、proto/extension_test.go:354 | 依赖 `-race` 构建与并发时序，不能保证确定性 |
| legacy 描述符兼容 | internal/impl/legacy_test.go:339,607、internal/testprotos/legacy/* | 依赖历史版本 protoc 生成物与 gzip 描述符字节 |
