---
id: l11_slice_copy_overlap
dim: L11_compiler_opts
priority: P2
sources:
  - klauspost-compress@v1.17.11: s2/s2_test.go:1528 expand 中循环 copy(x, src) 填充分片
  - klauspost-compress@v1.17.11: snappy/snappy_test.go:1038 同型 expand 分片拷贝
  - fasthttp@v1.59.0: client_test.go:2277 copy(p, r.s[r.n:]) 流式读取到调用方缓冲
tags: [slice-copy, overlapping-copy, memmove, copy-return, string-bytes, shallow-copy, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 6 轮；TORTURE_ROUNDS=100 时 600 轮）"
---

# 场景

模拟压缩 expand 分片与流式读取中的 `copy`：每轮 24..63 个元素，分别做重叠区间前向拷贝 `copy(s[2:], s[:n-2])`、后向拷贝 `copy(s[:n-2], s[2:])`、独立目标拷贝并保留尾部哨兵、短源/短目标返回长度、string→[]byte、nil 源/目标，以及两段 copy 实现的重叠旋转。另验证 `[]cpNode` 浅拷贝共享指针目标，并在 GC 后检查共享对象仍存活；全部轮次结果保留到最终 GC 后复核。

# 期望行为

1. 前向/后向重叠拷贝与“先快照源再逐元素写”的参考模型一致，返回 `n-2`；
2. 独立目标拷贝返回 n，源元素正确，目标前后哨兵未被覆盖；
3. 短目标返回目标长度 5，短源返回源长度 3 且目标尾部保持零值；
4. string→[]byte 拷贝返回字节长度且内容一致；
5. nil 目标、nil 源、nil 到 nil 的 copy 均返回 0；
6. 三索引目标仅写 len 范围，cap 内哨兵不变；
7. 两段重叠 copy 的旋转结果等于逐元素模运算参考；
8. struct 切片拷贝后指针字段共享，通过副本修改指针目标对源可见，GC 后仍可解引用；
9. 所有保留的前向/后向/旋转/独立目标结果在最终 GC 后逐元素不变；
10. 最后一行输出 `ALL PASS l11_slice_copy_overlap`。

# 失败判读

- `forward overlapping copy` / `backward overlapping copy`：重叠拷贝未按 memmove 语义处理；
- `separate copy and destination tail` / `three-index destination sentinels`：越界写或哨兵被覆盖；
- `copy returns ...`：返回长度计算错误；
- `string to byte copy`：字符串源拷贝错误；
- `overlap rotation`：两段 copy 的边界/方向错误；
- `struct copy shares pointer field` / `struct pointer fields after GC`：浅拷贝语义或对象保活错误；
- `retained copy result after GC`：保留切片底层数组被回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮长度 24..63、数据 salt 与旋转量，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（6 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_slice_copy_overlap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_slice_copy_overlap/*.go
UAF_SEED=1 /tmp/case_n
```
