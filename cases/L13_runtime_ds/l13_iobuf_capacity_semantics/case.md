---
id: l13_iobuf_capacity_semantics
dim: L13_runtime_ds
priority: P0
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:38 Put 错误尺寸缓冲后 Get 仍返回期望桶容量
tags: [L13, buffer-pool, size-class, capacity, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 pgx `iobufpool` 的 size-class 语义（纯内存自建池，不 import 三方库）：桶为 256/512/1024/2048/4096B。`Get(n)` 取最小可容纳桶（`n<=0` 时取 256B 桶，负值返回 nil）；`n` 超过最大桶时精确分配 `cap==n`。`Put` 只接受容量恰好等于某个桶的缓冲，其它尺寸（含 nil、300B、100B、5000B）一律丢弃，不得污染后续 `Get`。GC 插在池填满后、churn 期间和 drain 前，验证池中 slice 引用的底层数组仍然有效。

# 期望行为

1. 边界请求 0/1/255/256/257/511/512/513/1023/1024/1025/2047/2048/2049/4095/4096 返回对应最小桶容量，4097/8192 返回精确容量；返回长度均为 0；
2. 每轮 Put 后池中每类桶只保留一个缓冲（共 5 个），超桶尺寸的缓冲被丢弃；
3. Put cap=300/100/5000/nil 全部返回 false，池保持为空；随后 Get(256)/Get(4096) 得到全新零值桶；
4. 超尺寸 Get(5000) 返回 cap=5000；写入标记后 Put 被拒，再次 Get(5000) 得到新的零值缓冲；
5. Get(-1) 返回 nil；
6. 正确尺寸缓冲 Put 后被复用（len 重置为 0，底层标记字节保留），不同 class 互不串用；
7. GC 后池内缓冲容量仍正确、可继续借还；
8. 400 次随机尺寸 churn（含负值）后，池内条目数等于被触达的 class 数，drain 后归零；
9. 最后一行 `ALL PASS l13_iobuf_capacity_semantics`。

# 失败判读

- Get 容量偏小/偏大：桶选择（最小可容纳）或 `n>4096` 的精确分配错误；
- 错误尺寸 Put 后 Get 拿到脏缓冲：Put 未按 cap 校验就入桶；
- 复用标记丢失：桶里的 slice 未被正确扫描，底层数组被回收/清空；
- GC 后 cap 变化或崩溃：池持有的 `[][]byte` 生命周期问题（UAF）。

# 参数

- `UAF_SEED`：随机 churn 尺寸序列的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_iobuf_capacity_semantics/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
