---
id: l03_string_bytes_convert_loop
dim: L03_async_preempt
priority: P2
sources:
  - klauspost-compress@v1.17.11: zlib/writer_test.go:144 表驱动循环中先把 string 转成 []byte 再交给压缩器
  - nats-server@v2.11.0: server/client_test.go:193 循环内对读缓冲执行 string(buf[:n]) 后做子串计数
tags: [L03, async-preempt, string-bytes-conversion, copy-semantics, immutability, append, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

64KiB 确定性字节缓冲在循环中做双向转换：每轮 `s := string(b)` 后修改 b，验证字符串是独立副本；`b2 := []byte(s)` 后修改 b2，验证字符串不被反向影响；每 4 轮 append 8 字节后再转一次字符串并检查长度/边界。每 64 轮在 s/b2 仍存活时分配垃圾并显式 `runtime.GC()`，GC 后重新做 FNV 校验。结束后用独立的字节级模拟核对转换结果累加值与转换次数。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_string_bytes_convert_loop`；
2. `string(b)` 的结果不受后续 b 修改影响，`[]byte(s)` 的修改不影响 s；
3. 每轮 s/b2 的 FNV 哈希与其来源一致，append 后的边界字节正确；
4. 显式 GC 后 s/b2 内容与变异位置字节保持不变；
5. 转换次数等于 `2*rounds + ceil(rounds/4)`，显式 GC 次数等于 `ceil(rounds/64)`；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: string/byte conversion checksum`：转换结果错误，或转换被错误优化成别名；
- `FAIL: conversion copies and immutability`：string/[]byte 共享底层导致变异可见；
- `SIGSEGV` / `fatal error: found bad pointer`：字符串头/切片指针跨 GC 未被正确扫描；
- 超时：大缓冲哈希或 append 规模异常。

## 参数

- `UAF_SEED`：决定缓冲内容、变异位置步长/偏移与累加初值；失败信息带出种子；
- `TORTURE_ROUNDS`：放大默认 1024 轮（上限 65536）。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_string_bytes_convert_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8192 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
