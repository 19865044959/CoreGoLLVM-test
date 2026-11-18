---
id: l17_spill_many_ptr_args
dim: L17_stackmap
priority: P2
sources:
  - nats-server@v2.11.0: server/mqtt_test.go:2155（testMQTTPublish 9 参数，含 net.Conn/指针/slice）
  - nats-server@v2.11.0: server/jetstream_helpers_test.go:791（createJetStreamClusterEx 9 参数，含回调/指针）
tags: [L17, stackmap, spill, register-abi, many-args, pointer-args, GC, TORTURE_ROUNDS, UAF_SEED]
requires: []
runtime: "<10s（默认 240 轮；TORTURE_ROUNDS 放大轮数，上限 2000）"
---

# 场景

noinline `mix14` 接收 14 个 `*item` 加 string、`[]*item`、`map[string]*item`、`func() *item`，参数远超 amd64 整数寄存器数量，必然在 callee 栈上留下溢出槽。callee 在参数存活期间两次 GC，并分别用 `fold14` 求 checksum 对比；`(*spiller).blend` 再以 10 个指针参数 + slice/closure 走同一路径。caller 返回后 GC，逐项校验 14 个节点、list、map、闭包捕获指针与 receiver 字段。

# 期望行为

1. `mix14` 两次 checksum 一致且等于构造期望和；
2. `blend` 返回期望和，receiver `keep` 在方法内 GC 后仍有效；
3. caller 的 14 个节点及 `next`、list、map、闭包捕获指针在返回后 GC 仍正确；
4. 最后一行 `ALL PASS l17_spill_many_ptr_args`，退出码 0。

# 失败判读

- `mix14 checksum changed across GC`：某溢出参数槽未被标记，GC 后读到垃圾；
- `argN nil` / name corrupted：寄存器/栈溢出槽映射错位；
- `blend receiver keep`：方法 receiver 与栈上参数混用时漏标；
- 仅 clobberfree/efence 下失败：UAF 已发生；
- 崩溃：spill 槽偏移或指针位图错误。

# 参数

- `UAF_SEED`：决定每轮基数偏移；失败信息带种子。
- `TORTURE_ROUNDS`：轮数倍数（默认 240×，上限 2000），用于长压测。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_spill_many_ptr_args/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=4 UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
