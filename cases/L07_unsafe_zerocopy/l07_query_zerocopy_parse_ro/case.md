---
id: l07_query_zerocopy_parse_ro
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - fasthttp@v1.59.0: uri_test.go:468 TestNoOverwriteInput Parse 不得原地改写调用方输入 buffer
  - fasthttp@v1.59.0: args_test.go:150 TestArgsPeekMulti PeekMulti 返回共享子切片
  - fasthttp@v1.59.0: uri_test.go:34 TestURIAcquireReleaseConcurrent 池化 URI 并发解析/复用
tags: [L07, zero-copy, query-parser, percent-decode, input-immutable]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

仿 fasthttp Args/URI 的零拷贝 header/args（查询串键值对）解析：按 `&`/`=` 记录 key/value 在输入 buffer 中的偏移，未编码的 key/value 用 `unsafe.String` 直接引用输入；含 `%XX`/`+` 的值解码到独立新缓冲。每轮解析后逐字节比较输入与副本，校验零拷贝指针偏移、编码值不指向输入、重复 key、空值，并在多轮后只保留 string 视图跨 GC，验证各轮输入/解码缓冲被正确保活。

# 期望行为

1. 每轮解析出 7 个键值对：a(one-XX)、b(he%20llo-XX)、c(空)、d(%E4%B8%96-XX)、a(two-XX)、e(plain-N)、f(plus+sign-X)；
2. 输入 []byte 与解析前副本逐字节相等（解析零写入）；
3. 所有非空 key 的 `unsafe.StringData` 指向输入对应偏移；
4. 未编码 value 指向输入对应偏移；编码 value 指针不等于输入区间，含 `%` 的值解码后长度改变；
5. 重复 key a 的两个值不同且正确，空值 c 长度为 0；
6. 每 8 轮显式 GC + 垃圾分配，视图内容不变；
7. 保留轮次的 key/value 视图在最终 GC 后仍等于各自轮次的期望值（输入 slice 头已不可达）；
8. 最后一行 `ALL PASS l07_query_zerocopy_parse_ro`。

# 失败判读

- 输入与副本不等：解析器原地修改输入（如就地解码/去空白）；
- 零拷贝别名检查失败：key/value 被复制，不再零拷贝；
- 编码 value 指针落在输入区间：解码写入了输入而非独立缓冲；
- GC 后视图错乱/efence 崩溃：string 视图未保活输入/解码缓冲；
- 仅 stress/k8s 失败：视图存活与并发标记的写屏障/栈映射问题。

# 参数

- `UAF_SEED`：决定标签数值与每轮 plain 值，失败信息带种子与轮次；
- `TORTURE_ROUNDS`：解析轮数，默认 120；每 8 轮 GC 并复验，放大可做长压测。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_query_zerocopy_parse_ro/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=2000 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
