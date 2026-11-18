---
id: l12_big_init_table
dim: L12_init_link
priority: P2
sources:
  - fiber@v2.52.6: path_testcases_test.go:28 init() 中构建大型路由测试表
  - fiber@v2.52.6: router_test.go:25 init() 读取并 unmarshal 路由 fixture 到包级变量
  - go-redis@v9.8.0: bench_decode_test.go:21 测试二进制启动时初始化包级字节表
tags: [L12, large-init-table, function-values, map, linked-list, payload-pointers, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 8192 项；TORTURE_ROUNDS 放大到 65536 项）"
---

# 场景

包级 `init()` 构建 8192 项（`TORTURE_ROUNDS` 放大到 65536）函数分派表：每项包含 `id`、名字、函数值、指向 payload 的指针和 `next` 链指针，同时建立 `map[string]*entry` 名字索引与单向链。该场景模拟 fiber 在 init 中构造的大型 path/route 测试表。主程序全量校验结构、调用每个函数值、与独立期望函数比对结果，多轮显式 GC 后复核指针身份，并在 8 个 goroutine 并发只读派发期间持续制造垃圾与 GC。

# 期望行为

1. `len(dispatch)==8192*TORTURE_ROUNDS`，名字索引大小一致；
2. 每项 id/名字/payload 字段正确，`byName[name]` 与切片元素是同一指针，`next` 链完整且尾项为 nil；
3. 对所有项调用函数值，结果与 `wantOp(i%8, arg)` 完全一致，总和相等；
4. 多轮显式 `runtime.GC()` 后 `head`、中间项指针身份与全表结构不变；
5. 并发只读派发 + GC 期间无非预期结果；
6. 最后一行 `ALL PASS l12_big_init_table`。

# 失败判读

- 表大小错误：init 中环境变量读取或分配错误；
- 名字/指针身份失败：init 期 map 写入或指针图被破坏；
- 函数值结果错误：函数值表在初始化/链接期被错误改写；
- GC 后 payload/next 失效：大初始化表未被完整扫描（gcprog/写屏障问题）；
- 并发期间失败：只读表被 GC 错误回收。

# 参数

- `TORTURE_ROUNDS=1..8`：表规模倍数，默认 1（8192 项），最大 8（65536 项）；
- `UAF_SEED`：并发 worker 的起始偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_big_init_table/*.go
TORTURE_ROUNDS=4 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
