---
id: l17_select_defer_cases
dim: L17_stackmap
priority: P2
sources:
  - quic-go@v0.50.1: connection_timer_test.go:12（idle/ACK/loss/pacing 四种 deadline 取最小值的 select 链）
  - nats-server@v2.11.0: server/ipqueue_test.go:253（通知 channel + select 驱动生产者/消费者）
tags: [L17, stackmap, select, send-case, closed-channel, nil-channel, defer-in-case, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 2 轮 × 6/4/4 条；TORTURE_ROUNDS 放大规模，上限 48 倍）"
---

# 场景

select 的四种路径与 defer 的组合，全部在 noinline 函数中执行并在 defer 内 GC：

1. **send case**：select 前注册 defer（按值捕获 record），`out <- r` 与 nil channel 竞争，send 后 GC；
2. **case-body defer**：两个预填充 channel 上随机 select，defer 注册在被选中的 case body 内，函数返回时 GC 并校验该 record；
3. **closed channel**：只面对已关闭 channel 与 nil channel，校验 `ok==false` 且零值 record，defer 计数；
4. **pre-select defer**：select 之前注册 defer，select 之后才给 `record` 变量赋值，defer 中 GC 后读取。

## 期望行为

1. send 阶段每条记录都能入队，drain 后 id 顺序与 send 顺序一致，defer 校验全部通过；
2. 随机 select 阶段最终从 A/B 各取走 nRecv 条，id 总和等于预填充总和（选择顺序不影响不变量）；
3. closed 阶段三次调用均返回 `ok==false`/零值，defer 计数恰好 +3；
4. pre-select 阶段 nPre 条全部 handled 且 id 和正确，排空后 default 路径返回 `handled==false`；
5. 所有 defer 内 GC 后 record 的 head/next/list 均完好；
6. 最后一行 `ALL PASS l17_select_defer_cases`，退出码 0。

## 失败判读

- `send-defer` / `caseA-defer` / `caseB-defer` / `pre-defer`：defer 记录或 case 局部变量栈槽未被扫描；
- `select sum`/`seenA` 不符：消息丢失/重复，或 defer 内累加读到被破坏字段；
- `closed channel returned non-zero`：closed 路径的零值语义异常；
- `nil channel selected`：select 就绪集构造异常（非 GC 问题）；
- 仅 clobberfree 下失败：chan 缓冲/栈副本对象被提前回收。

## 参数

- `UAF_SEED`：决定每轮 base；
- `TORTURE_ROUNDS=N`：规模放大 N 倍（默认 1，上限 48），失败输出带种子。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_select_defer_cases/main.go
UAF_SEED=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
