---
id: l01_generic_fsm
dim: L01_generics
priority: P0
sources:
  - pgx@v5.7.4: pgxpool/pool_test.go:111 Acquire/Release 基本生命周期状态机
  - go-redis@v9.8.0: internal_test.go:302 withConn 正常/超时/普通错误后连接归还或移除状态机
  - pgx@v5.7.4: rows_test.go:47 扫描错误后 Rows 进入 fatal 粘滞状态，后续读取报同一错误
tags: [L01, generics, fsm, state-machine, comparable, transition-table, history, rejection, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

泛型状态机 `Machine[S comparable, E comparable, T any]`：状态/事件双类型参数 + 独立泛型负载，转换表为 `map[S]map[E]S`，记录完整转移历史与拒绝次数。状态表模拟连接/协程的 new→ready→running→paused→stopped 生命周期，非法事件不得改变状态。

先用固定非法事件覆盖拒绝路径，再以 UAF_SEED 驱动的 LCG 产生 160–199 个事件，与独立引用模型逐步比对接受结果、当前状态、拒绝计数与历史长度；每 32 步显式 GC 并校验负载指针与 blob。结束后校验历史链首尾衔接、四元组一致。最后实例化 `Machine[string, string, pcell]`，验证非整数状态/事件类型。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_fsm`；
2. 每条事件的接受/拒绝结果与模型一致，拒绝事件不改变状态；
3. 每步状态、拒绝计数、历史长度与模型一致；历史中相邻步骤 `from == prev.to`；
4. GC 后 `payload` 指针身份、id/name/blob 不变；
5. 重复注册同一 (状态, 事件) 返回 false 且不覆盖原目标；字符串状态机行为正确；
6. 在 clobberfree 等 profile 下结果一致。

# 失败判读

- `state got=... want=...`：转换表查找或状态写回错误，优先检查 map 泛型实例与复合键；
- `history chain broken`：历史记录/追加顺序异常；
- `payload ... corrupted`：泛型负载指针在 GC 后失效；
- 超时：随机序列异常或 GC 饥饿（本用例无并发，正常应秒级完成）。

## 参数

- `UAF_SEED`：事件序列种子与步数（160–199）。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_fsm/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
