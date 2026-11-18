---
id: l04_gomaxprocs_read_set
dim: L04_scheduler_stack
priority: P0
sources:
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 按 runtime.NumCPU 分片的长循环计数
  - tailscale@v1.80.2: net/connstats/stats_test.go:82 NumCPU 个 goroutine 并发发包与周期聚合
tags: [L04, GOMAXPROCS, NumCPU, runtime, workers, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 tailscale 分片计数与 connstats 中"按 CPU 数决定并发度"的读取/设置路径：保存初始 `runtime.GOMAXPROCS(0)`，依次切换到 1、2、4 和旧值，每次校验返回值与当前值，并在该设置下运行 8 个固定迭代 worker（各持有 2 节点链、周期性 Gosched）+ 显式 GC。另起一个读者 goroutine 在切换期间持续读取 `GOMAXPROCS(0)`，所有观测值必须属于合法集合。

# 期望行为

1. 初始 `GOMAXPROCS(0) >= 1`、`NumCPU() >= 1`，`GOMAXPROCS(-1)` 不改变设置；
2. 每次 `Set(p)` 返回上一个值，随后 `GOMAXPROCS(0) == p`；
3. 4 个设置下的工作负载校验和全部等于独立公式，链在 GC 后完整；
4. 并发读者观测到的值始终属于 `{1,2,4,旧值}`，无撕裂值；
5. 结束时 `GOMAXPROCS(0)` 恢复为旧值；
6. 进程退出码为 0，最后一行输出 `ALL PASS l04_gomaxprocs_read_set`。

# 失败判读

- `GOMAXPROCS(0)=... after set ...`：设置/查询不一致；
- `returned unexpected previous value`：返回值语义错误；
- `torn GOMAXPROCS value`：并发读写时读到非法值；
- `was not restored`：用例污染 runner 的 k8s/single profile；
- 校验和失败：worker 在指定 P 数下未完整执行。

# 参数

- `UAF_SEED`：工作负载链值种子；
- 固定 4 个设置档位，无 TORTURE 放大参数。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_gomaxprocs_read_set/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOMAXPROCS=1 /tmp/case
```
