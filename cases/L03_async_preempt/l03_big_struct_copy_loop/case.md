---
id: l03_big_struct_copy_loop
dim: L03_async_preempt
priority: P2
sources:
  - quic-go@v0.50.1: config_test.go:136 用反射枚举 Config 字段并 Clone：函数字段按引用复制，非函数字段深拷贝，修改副本不影响原值
  - etcd@v3.5.0: server/etcdserver/server_test.go:1237 应用循环与快照发送并发交错 101 轮，GC 插在快照拷贝前后
tags: [L03, async-preempt, large-struct, value-copy, memmove, stack-map, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

每轮把一个约 3KiB 的大结构体（`[384]int64` + 指向堆节点的指针 + string 头 + id）经 `//go:noinline` 函数整体按值传入并返回。调用返回后先显式 `runtime.GC()`（每 8 轮一次），再解引用副本里的 `p` 指针并校验各字段，迫使大结构体中的指针位图在栈上跨 GC 被扫描。另有 GC pump 并发执行 8 轮 GC。默认 4096 轮，`TORTURE_ROUNDS` 可放大。

# 期望行为

1. 进程退出码为 0；
2. 最后一行输出 `ALL PASS l03_big_struct_copy_loop`；
3. 每次拷贝的 `a[0]`、随机下标字段、`p`、`tag`、`id` 与源结构体一致（`a[0]` 按约定 +1）；
4. 拷贝结果的累加校验和等于源字段参考和；
5. 最后一轮副本的 `p` 在最终 GC 后仍指向正确节点，字段完好；
6. 每 8 轮显式 GC 一次，并发 GC pump 至少完成一轮；
7. 全部源结构体与节点在 GC 后保持原值；
8. 在 base/clobberfree/efence/k8s 等 profile 下输出与校验结果一致。

## 失败判读

- `FAIL: copied struct fields and pointer`：按值拷贝丢字段或指针被 GC 清零，优先查大结构体拷贝/栈映射；
- `FAIL: big-struct copy checksum after GC`：拷贝结果跨 GC 后读数错误；
- `FAIL: last copy keeps valid pointer after final GC`：结构体中的指针未被栈映射保活；
- `SIGSEGV` / `fatal error: found bad pointer`：大栈帧指针位图错误。

## 参数

- `UAF_SEED`：决定结构体访问步长与偏移；失败信息带出种子；
- `TORTURE_ROUNDS`：替换默认 4096 轮，用于放大拷贝次数与 GC 次数。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_big_struct_copy_loop/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=16384 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
