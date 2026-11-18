---
id: l12_oncefunc_values_nested
dim: L12_init_link
priority: P2
sources:
  - tailscale@v1.80.2: util/codegen/codegen_test.go:454 var namedTestTypes = sync.OnceValues(...) 包级懒值
  - grpc-go@v1.71.1: balancer/pickfirst/pickfirstleaf/pickfirstleaf_ext_test.go:1633 sync.OnceFunc 关闭 stop channel
  - kitex@v0.13.0: internal/configutil/once_test.go:25 TestOnceOrPanic 首次成功、再次 panic
  - go@1.24.1: src/sync/oncefunc.go:10 OnceFunc/OnceValue/OnceValues panic 后每次调用都以同值 panic 的契约
tags: [L12, sync.Once, OnceFunc, OnceValue, OnceValues, nested-lazy, panic, concurrency, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 8 worker × 300 迭代；TORTURE_ROUNDS 1..8 放大到 64 × 2400）"
---

# 场景

包级 `sync.OnceValue` 组成嵌套懒图：`derive` 的 Once body 内调用 `base`；另有 `OnceValues` 成功值、缓存错误、`OnceFunc` 每次调用都 panic 但 body 只执行一次、返回 nil 的 `OnceValue`，以及普通零值 `sync.Once`。并发首次调用者必须看到同一指针/字符串/错误身份，同时主 goroutine 显式 GC。

## 期望行为

1. 首次调用后 `baseCalls==deriveCalls==1`，`derive().id==2`，base 与 derive 是不同节点但 derive 缓存同一指针；
2. `base()` 返回 id 1、payload `base-payload`；重复调用不增加 body 次数；
3. `OnceValues` 返回 `pair-7,nil` 并缓存；错误版本每次返回同一哨兵 `cachedErr` 且 body 只跑一次；
4. nil 结果被缓存（`nilValue()==nil` 且调用计数为 1）；
5. 三次调用 `failFn` 均恢复出 `once-fn-boom`，body 计数仍为 1；普通 Once body 只执行一次；
6. 并发阶段所有 worker 看到同一 `*node` 指针、payload 首字节、pair 字符串与错误身份；所有 body 计数恰为 1；
7. `TORTURE_ROUNDS` 放大 worker/迭代数并保持上述断言；
8. 显式 GC 后指针身份、payload、错误身份、nil 结果与 body 计数不变；
9. 最后一行 `ALL PASS l12_oncefunc_values_nested`。

## 失败判读

- body 计数 >1：Once/OnceValue 的成功发布或 happens-before 错误；
- 并发阶段看到不同指针/字符串：缓存值被重写或未正确扫描；
- panic 值不是 `once-fn-boom` 或首次后不再 panic：OnceFunc panic 语义错误；
- 错误身份/`errors.Is` 失败：OnceValues 缓存槽被覆盖；
- GC 后值变化：全局 funcval/闭包捕获的指针未被扫描；
- 超时：嵌套 Once 自等待或死锁。

## 参数

- `TORTURE_ROUNDS`（默认 1，上限 8）：worker 数 = 8×rounds（上限 64），迭代 = 300×rounds（上限 2400）；
- `UAF_SEED`：并发读取的初始偏移，失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_oncefunc_values_nested/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=7 /tmp/case
```
