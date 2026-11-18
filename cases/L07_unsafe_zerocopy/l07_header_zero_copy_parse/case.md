---
id: l07_header_zero_copy_parse
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - fasthttp@v1.59.0: header_test.go:325 RawHeaders 返回原始 header 块，key/value 索引共享同一缓冲
  - fasthttp@v1.59.0: uri_test.go:468 Parse 不得原地改写调用方输入 buffer
tags: [L07, unsafe, header-parser, zero-copy, slice-offset]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

仿 fasthttp 的 header 解析：扫描 CRLF 行，用 `bytes.IndexByte` 定位冒号，只记录 key/value 在输入缓冲中的偏移，`key` 用 `unsafe.String` 零拷贝引用输入，`value` 是输入的子切片；OWS（空格/制表符）只调整偏移，绝不原地改写。每轮用新缓冲解析并逐字节对比输入副本，周期性 GC 后再次校验；最后保留一组视图跨越 GC 读取。

# 期望行为

1. 每轮解析出的 7 个 header 的 key/value 内容与期望完全一致，空值 header 保持 0 长度；
2. 输入缓冲与解析前副本逐字节相等（解析零写入）；
3. key 的 `unsafe.StringData` 与 value 的 `unsafe.SliceData` 都指向输入缓冲对应偏移，证明零拷贝；
4. GC 后视图内容、别名关系、大小写敏感查找（`X-Trace-Id`）均正确，缺失 key 返回 false；
5. 最后一行 `ALL PASS l07_header_zero_copy_parse`。

# 失败判读

- 输入与副本不等：解析器原地修改了输入（如就地小写化/去空白）；
- 别名检查失败：key/value 被复制，不再是零拷贝；
- GC 后内容错乱/efence 崩溃：视图未保活输入缓冲，或 `unsafe.String` 的长度/起始偏移计算错误；
- 只在 stress/k8s 失败：视图存活与并发标记的写屏障/栈映射问题。

# 参数

- `UAF_SEED`：决定 `X-Seed-Run` 的取值（`seed%97`），失败信息带种子；
- `TORTURE_ROUNDS`：解析轮数，默认 200；每 8 轮执行一次显式 GC 并复验。放大该值可做长压测。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_header_zero_copy_parse/*.go
UAF_SEED=1 TORTURE_ROUNDS=2000 GODEBUG=clobberfree=1 /tmp/case
```
