---
id: l07_largepayload_segmented_zerocopy
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - protobuf-go@v1.36.6: internal/impl/lazy_buffersharing_test.go:91 lazy 消息与输入 buffer 共享，不必要拷贝不得发生
  - klauspost-compress@v1.17.11: s2/writer_test.go:579 BufferDone 按 10B..8MB 档位归还缓冲，验证所有权与复用契约
  - badger@v4.5.0: stream_writer_test.go:582 10MB value 经大缓冲写入后 GC 再读回
tags: [L07, unsafe, large-payload, zero-copy, string-header, liveness]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

大 payload 的零拷贝分块持有：构造 2MiB 以上的确定性缓冲，按 64KiB 切成段，每段用
`unsafe.String(unsafe.SliceData(chunk), len(chunk))` 生成直接引用输入的 string，段描述只保留
偏移、长度与 FNV-1a 校验和。每轮丢弃缓冲变量后 GC，并复核此前所有轮次保留下来的分段视图。
模拟 protobuf lazy buffer sharing、压缩库大缓冲归还与 LSM 大 value 写入的持有方式。

## 期望行为

1. 段数、每段长度、相邻段偏移连续性与 `{data,len}` 视图地址关系正确；
2. 每段 FNV 校验和等于按输入模式独立计算的期望值，首/中/末字节抽样一致；
3. 整块 payload 校验和等于独立重算值；
4. 改写输入字节后，对应段视图立即观察到变化；恢复后校验和复原；
5. 丢弃缓冲变量并显式 GC 后，所有轮次保留的分段 string 仍可读且校验和不变；
6. `TORTURE_ROUNDS` 放大轮次（默认 4，上限 16）后不出现悬挂视图；
7. 末行 `ALL PASS l07_largepayload_segmented_zerocopy`，失败退出码 1。

## 失败判读

- 段视图内容与模式不符：`unsafe.String` 长度或偏移计算错误；
- GC 后读段崩溃/efence 报错：string 头未被当作根，或大对象扫描位图漏标；
- 输入改写不可见：视图实际发生了复制，零拷贝语义丢失；
- 相邻段地址不连续：段切分/偏移计算错误；
- 仅 k8s/stress 失败：GOGC=1 下大对象存活/标记问题。

## 参数

- `UAF_SEED`：决定 payload 大小（2MiB/2.25MiB/2.5MiB）与字节模式，失败信息带种子；
- `TORTURE_ROUNDS`：轮次放大（默认 4，钳制到 1..16），每轮保留一组视图。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_largepayload_segmented_zerocopy/*.go
UAF_SEED=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
