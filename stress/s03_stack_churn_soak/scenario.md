---
id: s03_stack_churn_soak
priority: P1
covers: [L04, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

栈增长/展开长跑：多 worker 反复执行 1500 层递归（每层 32 个指针槽 + 局部指针），GC 落在增长与展开点；穿插 panic/recover、递归闭包、64KB 大帧与 FreeOSMemory 收缩。

## 不变量

1. 每层展开时本帧 `buf[0]/buf[31]/local` 与传入指针的关系不变；
2. 顶层递归 checksum 与公式一致；
3. panic payload 在 recover+GC 后可读且值正确；
4. 递归闭包与大帧 checksum 正确；
5. 心跳持续增长，watchdog 无停滞；
6. 结束打印 `STRESS PASS s03_stack_churn_soak`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4）
- `STRESS_DEPTH`（默认 1500）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`

## 失败判读

- `phase=deep/closure/bigframe ... corrupted`：帧内指针被破坏或栈复制未修正；
- `phase=panic ...`：展开路径 payload 损坏；
- `watchdog stalled`：死锁/GC 饥饿；
- 只在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress STRESS_SECONDS=0 FILTER=s03 ./run.sh
```
