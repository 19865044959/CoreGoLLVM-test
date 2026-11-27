---
id: y10_mixed_ret_bombard
priority: P1
covers: [L17, L18, L01]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

y01/y03/y05 三种返回槽形态的组合终验轰炸。worker 每轮按 `shapeNext(seed, round)` 轮换形态：

- **anon**（y01 形状）：528B Big 匿名返回，dirtyRec 同深度铺垫（d=3+i%96），返回后 noinline GC 再分字段消费校验；
- **gen**（y03 形状）：`Box[T].Get() T` 泛型返回（多类型实例化），经 itab 字典路径或直接调用，返回后 GC 再消费；
- **iface**（y05 形状）：大 receiver 拷贝 + itab 派发方法返回大值，方法值/方法表达式/直接调用三变体。

形态切换点强制 `runtime.GC()` + 脏栈铺垫；gcChurn 常驻（GOMAXPROCS=1 时让出）。metrics 按形态分计数；固定轮数下与 PRNG 重算的期望分布精确比对（轮换确定性校验）。

## 不变量

1. 每形态返回值分字段 canary 校验（GC 后消费）；
2. 形态计数与 shapeNext 重算一致（轮换不被破坏）；
3. 固定轮数 ≥12 时三形态各 ≥ fixed/4（覆盖充分性）；
4. goroutine 回基线；心跳随轮增长。

## 失败判读

- canary mismatch：对应形态返回槽未清零/被改写（编译器 bug）；
- 形态计数不符：轮换逻辑被优化破坏；
- 进程崩溃（fatal error）：返回槽漏标被 GC 扫到脏值。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4）、`STRESS_FIXED_ROUNDS`、`STRESS_HEARTBEAT`、`UAF_SEED`

## 运行

```bash
SUITE=stress FILTER=y10 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=100 PROFILES=base ./run.sh
```
