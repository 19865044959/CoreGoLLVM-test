---
id: l07_binary_tlv_zerocopy
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - pgx@v5.7.4: pgproto3/chunkreader_test.go:9 已缓冲数据不重复读底层 reader，耗尽后重置
  - pgx@v5.7.4: pgproto3/frontend_test.go:119 消息体长度超过上限立即失败，不分配大缓冲
  - sonic@v1.13.2: ast/parser_test.go:53 惰性 Parse 只保存输入 span
  - goccy-json@v0.10.5: path_test.go:12 路径抽取返回输入子切片
tags: [L07, zero-copy, binary-parser, tlv, nested, read-only, liveness]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

零拷贝只读 TLV 解析器：输入为 `[tag u16][len u16][payload]` 记录流，容器记录递归解析嵌套 TLV。叶子 payload 用 `unsafe.String` 直接引用输入子切片，不做任何原地改写；覆盖重复 tag、空 payload、含 NUL 的二进制 payload、嵌套记录、畸形长度/截断头/深度超限以及多轮保留视图跨 GC。

## 期望行为

1. 每轮解析出 10 条记录（6 条顶层 + 4 条嵌套），magic/leaf/empty/binary/container 的 tag、len、value、depth 全部正确；
2. 输入 []byte 与解析前副本逐字节相等，解析零写入；
3. 所有非空 value 的 `unsafe.StringData` 等于对应 raw 的起始地址，raw 与输入别名，证明零拷贝；
4. 重复 leaf 顺序与值正确，空 payload 长度为 0 且 value 为空，含 NUL 的二进制 payload 长度按字节计；
5. 第二次解析同一输入结果一致（只读）；
6. 畸形输入（声明长度越界、截断头、深度超限）返回错误、不 panic、不修改输入；
7. 多轮后丢弃全部输入缓冲，仅保留记录视图并 GC，raw/value 内容仍正确；放大 `TORTURE_ROUNDS` 后仍通过；
8. 末行 `ALL PASS l07_binary_tlv_zerocopy`。

## 失败判读

- 输入与副本不等：解析器原地改写输入（如就地解码/小写化）；
- 别名检查失败：value 被复制，零拷贝失效；
- GC 后视图错乱/efence 崩溃：string/[]byte 视图未保活输入缓冲；
- 记录数/深度不符：嵌套容器递归或边界判断错误；
- 畸形输入 panic/分配超大缓冲：长度校验先于分配，且必须使用剩余长度判定；
- 仅 stress/k8s 失败：视图存活与并发标记的写屏障/栈映射问题。

## 参数

- `UAF_SEED`：决定叶子/嵌套 payload 的编号与二进制末字节，失败信息带种子；
- `TORTURE_ROUNDS`：解析轮数，默认 80；每 8 轮显式 GC 并复验，放大可做长压测。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_binary_tlv_zerocopy/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=5000 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
