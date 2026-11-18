---
id: l12_large_initializer
dim: L12_init_link
priority: P2
sources:
  - go-redis@v9.8.0: bench_decode_test.go:21 包级大字节表在测试二进制启动时初始化
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/mocks_test.go:706 包级 rawDesc 大字节切片 + 懒初始化
tags: [L12, large-initializer, gcprog, static-data, map, pointers, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 12000 项 map；TORTURE_ROUNDS 放大到 240000 项）"
---

# 场景

包级静态数据包含 128 项 `uint64` 数组、16×8 指向包级 `int64` 变量的指针矩阵（触发静态 gcprog 数据）与 16KB 字节表；另有一个在包级变量初始化阶段按 `TORTURE_ROUNDS` 构造的大 map（每项含 slice、string、next 指针）及其反向链表。init 阶段先做一次整体校验，主程序多轮 GC 后再全量复核。

# 期望行为

1. 静态数组 128 项全部匹配生成公式；指针矩阵每格非 nil、解引用值正确、跨行指向同一包级变量；
2. 包级标量变量与指针矩阵别名一致，修改 `lv0` 后矩阵多格可见，GC 后仍可见；
3. 16KB 字节表长度、边界字节与校验和正确；
4. 大 map 项数、id 和、head、反向链长度与每项 payload 正确；
5. init 阶段的 `bigInitBad` 为 false（init 可见完整大对象）；
6. 多轮 GC + 垃圾分配后静态表、指针矩阵、字节表、大 map 全部复核通过；
7. 默认 12000 项；`TORTURE_ROUNDS` 线性放大 map 规模与 GC 轮数；
8. 最后一行 `ALL PASS l12_large_initializer`。

# 失败判读

- 静态数组/指针矩阵错误：静态初始化数据（含 gcprog）生成或重定位错误；
- 大 map 项缺失/链断裂：包级 var 初始化阶段大对象构造不完整；
- GC 后指针矩阵/大 map 损坏：全局 gcprog 或写屏障错误；
- 失败行 `seed=` 可用于复现抽样 key 起点。

# 参数

- `TORTURE_ROUNDS`（默认 1，上限 20）：map 项数 = `12000 × rounds`，上限 240000；GC 轮数同步增加；
- `UAF_SEED`：决定 64 个 map 抽样项的起始 key 偏移；全量校验与顺序无关，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_large_initializer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
