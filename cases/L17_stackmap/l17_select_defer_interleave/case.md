---
id: l17_select_defer_interleave
dim: L17_stackmap
priority: P2
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:126（流取消与压缩标志两次 select 竞争）
  - fasthttp@v1.59.0: server_test.go:2914（TimeoutHandler 的 select/time.After 路径）
tags: [L17, stackmap, select, defer, chan-of-struct, per-iteration-capture, GC, TORTURE_ROUNDS, UAF_SEED]
requires: []
runtime: "<10s（默认 60 轮；TORTURE_ROUNDS 放大轮数，上限 512）"
---

# 场景

两个预填充 buffered channel 上反复 `select`，每次取出含指针的 `msg` 后立即注册两个 defer：一个按值传参（defer 记录保存 msg 副本），一个捕获 per-iteration 局部 `cur`。循环每 4 次在 select 前 GC；channel 排空后经 default 返回，全部 defer 在 deferreturn 阶段 LIFO 执行，每个 defer 内再 GC 并校验 msg 指针。第二轮只填一个 channel，验证 default 立即返回。

# 期望行为

1. 第一轮 count 恰为 16（8+8），total 等于两倍 id 和（循环累加 + defer 累加）；
2. 所有 defer 内 `msg.head`/`msg.list` 校验通过；
3. 第二轮 count=4、total=2×期望和；
4. select 的随机选择顺序不影响结果（只校验不变量）；
5. 最后一行 `ALL PASS l17_select_defer_interleave`，退出码 0。

# 失败判读

- `defer-captured` / `defer-arg`：defer 记录或闭包捕获的 msg 指针未被扫描；
- count/total 不符：有消息丢失/重复，或 defer 内累加读取到被破坏的字段；
- 仅 clobberfree 下 msg 指针变垃圾：chan 缓冲对象被提前回收；
- 超时：select/default 逻辑异常。

# 参数

- `UAF_SEED`：决定每轮 id 基数；失败信息带种子。select 自身的选择顺序由运行时决定，但校验只依赖不变量。
- `TORTURE_ROUNDS`：轮数倍数（默认 60×，上限 512），用于长压测。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_select_defer_interleave/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=8 UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
