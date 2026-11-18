---
id: l13_byte_pool_reuse
dim: L13_runtime_ds
priority: P2
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:71 TestPutGetBufferReuse 10 万次 Get/Put，GC 会掉池
  - badger@v4.5.0: y/y_test.go:301 TestAllocatorReuse Reset 后复用 byte slice 做批量 Copy/编码
  - pgx@v5.7.4: pgtype/bytea_test.go:49 TestDriverBytes 1000 行结果复用同一 byte slice
tags: [L13, sync.Pool, byte-buffer, size-class, zeroing, ownership, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟连接/序列化库的 byte 缓冲池：7 个 size class（256B..1MB），`sync.Pool` 每类独立存放 `*pooledBuf`：

1. 每轮随机租 48 个不同 class 的缓冲并写满模式/校验和，跨 GC 校验存活缓冲；归还一半后 GC，保留缓冲内容不变；
2. 再租 24 个，Get 时必须 `len==请求`、`cap>=请求` 且池中返回的缓冲前 64 字节已清零；
3. 4KB 类 240 次 Get/写模式/Put churn，每 40 次 GC，验证内容隔离；
4. 超过最大 class 的 2MB+123 缓冲直接精确分配，GC 后内容正确，归还时丢弃；
5. nil、错误 class/cap 的 Put 一律丢弃，统计 `gets == allocs + reuses`。

每轮显式 GC 会清空 sync.Pool，用例不依赖“一定复用”，只校验容量、清零与所有权不变量。`UAF_SEED` 决定尺寸与内容，`TORTURE_ROUNDS` 默认 3 轮。

# 期望行为

1. 所有 Get 返回的 `len` 精确、`cap` 足够，池中返回缓冲已清零；
2. 存活缓冲跨 GC 内容不变；Put 后再 GC 不影响仍被租用的缓冲；
3. 大缓冲精确分配且归还被丢弃；
4. 错误尺寸/nil Put 全部计入 drops，不污染池；
5. 每轮统计自洽，失败信息带 seed；
6. 最后一行 `ALL PASS l13_byte_pool_reuse`。

# 失败判读

- `dirty byte on Get` / `leaked data`：Put 未清零或混入其他 class；
- `rented buffer corrupted after GC`：池/GC 复用了仍存活的底层数组；
- `capacity too small` / `class mismatch`：size-class 归桶错误；
- `wrong-size/nil/direct puts not dropped`：Put 校验缺失；
- 超时：churn 或大缓冲清零路径异常。

# 参数

- `UAF_SEED`：尺寸/内容序列种子，失败信息附 seed；
- `TORTURE_ROUNDS`：轮数，默认 3，上限 20。

# 运行

```bash
go build -o /tmp/l13_byte_pool_reuse ./cases/L13_runtime_ds/l13_byte_pool_reuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_byte_pool_reuse
UAF_SEED=7 TORTURE_ROUNDS=8 /tmp/l13_byte_pool_reuse
```
