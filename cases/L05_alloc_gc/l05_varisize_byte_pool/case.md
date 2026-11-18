---
id: l05_varisize_byte_pool
dim: L05_alloc_gc
priority: P1
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:38 的 TestPutHandlesWrongSizedBuffers：错误尺寸 Put 后 Get 仍返回期望容量
  - klauspost/compress@v1.17.11: s2/writer_test.go:579 的 TestWriterBufferDone：多尺寸缓冲经 sync.Pool 归还并复用
tags: [L05, sync.Pool, variable-size, byte-buffer, reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

一个 sync.Pool 同时服务 1B 到 1MB+7 的变尺寸请求（不同于固定 size-class 池）。
每轮同时持有 8 个不同尺寸缓冲，写入标记并显式 GC，再校验内容；释放一半后按
完全不同的尺寸重新获取，检查 `len==n`、`cap>=n`、对象不重复、互不别名。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_varisize_byte_pool`；
2. 每次 Get 后 `len(buf)==请求尺寸`、`cap(buf)>=请求尺寸`；
3. 同时存活的缓冲指针互不相同，写其中一个不影响另一个；
4. 跨 `runtime.GC()` 与 GC 清池后，仍持有的缓冲内容与种子推导值一致；
5. 释放后按不同尺寸重新获取，旧内容不再可见（重新填满后校验全部字节）；
6. 池被 GC 清空后重新请求全部尺寸档，契约不变；
7. 在 base/clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `len/cap` 断言失败：池返回了容量不足的缓冲，或 len 设置错误；
- `duplicate live buffer`：同一对象被重复从池中取出；
- `content corrupted`：池化复用时旧数据串入新请求，或底层数组被提前回收；
- 崩溃：缓冲所有权/生命周期错误。

## 参数

- `UAF_SEED`：决定每轮请求的尺寸序列与填充模式；失败输出带种子；
- 无其他 TORTURE 参数；重复 192 轮。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_varisize_byte_pool/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
