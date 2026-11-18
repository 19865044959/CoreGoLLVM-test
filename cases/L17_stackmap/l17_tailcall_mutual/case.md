---
id: l17_tailcall_mutual
dim: L17_stackmap
priority: P2
sources:
  - nats-server@v2.11.0: test/service_latency_test.go:1226（service/stream import 递归解析，原栈溢出回归用例）
  - sonic@v1.13.2: issue_test/issue_recurse_test.go:16（自引用 JSONTime Marshaler 递归 marshal）
tags: [L17, stackmap, tail-call, mutual-recursion, interface-dispatch, closure-recursion, stack-growth, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 4 轮，深度 320/48/64；TORTURE_ROUNDS 放大轮数，上限 64）"
---

# 场景

三条尾位置调用链，每层把 `payload{*item, depth, sum}` 按值传给下一层，链式 `next` 指针只在各层栈帧中存活：

1. `stepA`/`stepB` 互相尾递归（深度 320，每 9 层 GC），基例 GC 后遍历 next 链校验长度；
2. `walkImpl.walk` 经接口 `walker`（receiver 内含 `self walker`）间接递归（深度 48，每 5 层 GC）；
3. 函数值闭包递归（深度 64，每 7 层 GC）。

返回值由逐层累加的 v 决定，caller 用闭式公式核对；所有路径结束后再 GC 校验根节点。

## 期望行为

1. `stepA/B` 返回 `expectAlternating(base,320)`（交替 +1/+2 的逐层和）；
2. 接口递归返回 `expectStep(base,48,3)`，receiver `keep` 跨每层 GC 不变；
3. 闭包递归返回 `expectStep(base,64,4)`；
4. 每个基例的 next 链长度等于 `depth+1`，节点 name 未被破坏；
5. 三条链结束后 GC，`root.v/name` 不变；
6. 最后一行 `ALL PASS l17_tailcall_mutual`，退出码 0。

## 失败判读

- `got=... want=...`：某层 payload 中的 `it` 指针被 GC 回收/错位，sum 与返回值偏离；
- `chain broken`：尾调用点或基例帧的链式指针漏标；
- `receiver keep corrupted`：接口 receiver 在尾递归中未被扫描；
- `self interface nil`：接口 receiver 字段记录异常（非 GC 问题）；
- 仅 clobberfree 下失败：UAF 已发生；崩溃可能出现在链遍历或 morestack。

## 参数

- `UAF_SEED`：决定每轮 base；
- `TORTURE_ROUNDS=N`：轮数乘 N（默认 1，上限 64）；深度固定 320/48/64。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_tailcall_mutual/main.go
UAF_SEED=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
