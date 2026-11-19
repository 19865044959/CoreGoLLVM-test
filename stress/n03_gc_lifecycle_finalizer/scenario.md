---
id: n03_gc_lifecycle_finalizer
priority: P0
covers: [L18, L05, L13]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

GC 生命周期/finalizer 长跑。每 worker 每轮执行“分配 → 丢弃 → `runtime.GC()`”循环：

- 普通对象（带 pointer payload）注册 finalizer：2 个作为 anchor 保持可达（不得被终结），其余失活；
- 带 interior pointer 字段的 holder：`mid = &base.pad[1]` 指向另一块分配的内部，holder 注册 finalizer，GC 后校验 `*mid`；
- tiny（`new(int8)`）与 zero-size（`new([0]int64)`）对象注册 finalizer；
- `sync.Pool` Put/Get 带 finalizer 的对象，周期性丢弃整个池；
- 独立 GC churn goroutine。

finalizer 通过带缓冲 channel 通知 collector goroutine；collector 维护已终结 id 位图（检测重复终结）与 live set（检测“可达对象被终结”）。结束后用一个 noinline 失活探针 + 超时 channel 验证 finalizer 至少能被执行。

来源：`go/src/runtime/mfinal_test.go:24-160`、`go/src/runtime/mcleanup_test.go:119-159,249-282`、`go/src/weak/pointer_test.go:127-197`、`go/src/runtime/pinner_test.go:162-196`、`go/src/runtime/gc_test.go:805-903`。tiny/zero-size finalizer 的“可能永不执行”行为见 `go/src/runtime/mfinal_test.go:56,245` 的 tinyalloc 注释。

## 不变量

1. 可达 anchor 对象只允许内容校验成功，绝不允许收到其 finalizer 通知（硬失败）；
2. 同一 id 不得被终结两次（位图检测）；
3. 每类 `finalized <= allocated`；
4. interior pointer holder 在 GC 后 `*mid`、`base.pad` 全部不变；
5. 探针对象失活后其 finalizer 必须在 10s 超时内到达（timeout channel）；超时判 FAIL；
6. 每轮各类终结数只记录并汇总，**不断言速率/比例**；tiny/zero 的终结数同样只记录（运行时可能永不执行）；
7. `sync.Pool` Get 返回非 nil 时对象内容必须完整；池丢弃后终结数只记录；
8. 每 worker `started == finished`；固定模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；
9. 结束打印 `STRESS PASS n03_gc_lifecycle_finalizer`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`
- `STRESS_FIXED_ROUNDS`（默认 32，仅 seconds=0）
- `finbuf` 固定 65536：finalizer 通知缓冲，满时非阻塞丢弃并计数（`fin_dropped`）

## 失败判读

- `可达对象被终结`：GC 在对象仍可达时执行了 finalizer（mark/写屏障/终结器队列错误）；
- `同一对象重复终结`：finalizer 重复入队/重复执行；
- `finalized > allocated`：终结计数越界；
- `interior pointer object corrupted`：内部指针未正确保活对象或字段被破坏；
- `probe ... 未在 10s 超时内执行`：finalizer 队列停滞/丢失（终结机制失效）；
- `pool ... pooled object corrupted`：sync.Pool 复用返回损坏对象；
- `goroutine baseline ... leak`：finalizer/collector/worker goroutine 泄漏；
- 只在 clobberfree/efence 下失败：finalizer/池对象 UAF。

## 运行

```bash
SUITE=stress FILTER=n03 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=n03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n03 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 PROFILES=base ./run.sh
```
