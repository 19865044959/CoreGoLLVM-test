---
id: l17_spill_iface_pair
dim: L17_stackmap
priority: P1
sources:
  - gorm@v1.30.0: tests/connpool_test.go:25（ExecContext 接收 ...interface{} 并原样转发）
  - grpc-go@v1.71.1: test/channelz_test.go:773（StreamingOutputCall 变参 opts ...grpc.CallOption，接口参数跨调用）
tags: [L17, stackmap, spill, register-abi, interface-pair, struct-by-value, morestack, noinline, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`pressure` 同时持有 8 个 `any` 局部（指针、含指针 struct 值、`[]*item`、字符串、nil、匿名 struct）、8 个 `any` 形参、2 个含指针的 box 形参和一个 `*item`。它在深度 30 的递归 `deep`（每帧 512B 局部数组，触发栈增长/复制）之后 GC，再逐个类型断言并解引用全部接口值；同时把同样参数传给 noinline `observe`，后者在自身帧内再做两轮 GC 与扫描。覆盖 iface type/data 两字与聚合字段在寄存器溢出到栈槽后的栈图。

`UAF_SEED` 决定每轮 base 与失败输出。

# 期望行为

1. pressure 内 local iface 对与形参在深递归 + GC 后全部可解引用，两次扫描和相同；
2. observe 的 11 个接口/聚合参数在两轮 GC 后 sum 与 pressure 记录的 args 期望一致；
3. 返回后 caller 的 `args` 切片与 `p/b0/b1` 指针仍有效；
4. 深度递归触发的 morestack/栈复制不破坏任何指针；
5. 退出码 0，最后一行 `ALL PASS l17_spill_iface_pair`。

# 失败判读

- `item/box/slice/anon ... corrupted`：iface data word 或聚合字段栈槽漏标；
- `locals/args sum ... want ...`：GC 后局部或形参的接口值被破坏；
- `observe sum ...`：跨调用溢出槽位扫描错误；
- `caller ... changed after call`：caller 帧接口值被破坏；
- SIGSEGV 在深递归后：栈复制未修正指针或漏扫。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 6 轮，递归深度 30。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_spill_iface_pair/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
