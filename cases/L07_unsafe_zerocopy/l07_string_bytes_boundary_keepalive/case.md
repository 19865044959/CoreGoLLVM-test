---
id: l07_string_bytes_boundary_keepalive
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: decoder/decoder_native_test.go:276 TestCopyString 对比别名/拷贝模式下修改输入 buffer 的可见性
  - gin@v1.10.0: internal/bytesconv/bytesconv_test.go:28 TestBytesToString 验证 []byte→string 零拷贝
  - fiber@v2.52.6: utils/convert_test.go:19 Benchmark_UnsafeString 零拷贝转换的别名语义
tags: [L07, string, bytes, lifetime, KeepAlive, alias-vs-copy]
requires: []
runtime: "<10s"
---

# 场景

零拷贝 `string<->[]byte` 转换的全部边界：前缀子切片、空串/空切片、含 NUL 的字节序列、父串丢弃后的子串、函数返回后源临时对象不可达、以及 `runtime.KeepAlive` 保护的参数跨显式 GC。用例用 `unsafe.String(unsafe.SliceData(b), len(b))` 与 `unsafe.Slice(unsafe.StringData(s), len(s))` 构造视图，并与 `string(b)` 的复制语义对照。

# 期望行为

1. 子切片转 string 后长度正确，数据指针等于源切片基址 + 偏移；
2. 改写源 []byte 时零拷贝 string 观察到变化，复制版保持原值；
3. 源 []byte 与子切片变量置 nil、显式 GC + 垃圾分配后，string 视图内容不变；
4. 子串视图的数据指针等于父串数据指针 + 偏移，父串置空并 GC 后仍可读；
5. 空 []byte、零长度带容量 []byte、空 string 的转换边界均得到 0 长度且不崩溃；
6. 含 NUL 的字节序列长度按字节计，GC 后逐字节一致；
7. 源 []byte 与中间 string 都只存在于被调函数内时，返回的 []byte 视图跨 GC 仍有效；
8. `runtime.KeepAlive(s)` 保护的 string 在函数内显式 GC 后派生字节可读；
9. 80 轮临时源转换、每 16 轮 GC，跨 GC 保留的 8 个视图内容各自正确；
10. 最后一行 `ALL PASS l07_string_bytes_boundary_keepalive`。

# 失败判读

- GC 后内容错乱或 efence 下 SIGSEGV：视图未让底层数组存活，或 string/切片头数据指针放错；
- 复制版随源变化：`string(b)` 被错误优化成别名；
- 零拷贝视图不随源变化：转换意外复制，零拷贝失效；
- `KeepAlive` 路径失败：编译器提前结束参数生命周期（liveness/栈位图问题）；
- 仅在 stress/k8s 失败：GOGC=1 并发标记下视图未被当作根。

# 参数

- `UAF_SEED`：决定子切片偏移（0..7）、长度（24..39）、临时源长度与批量轮次内容偏移，失败信息带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_string_bytes_boundary_keepalive/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
