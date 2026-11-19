---
id: n07_cgo_storm
priority: P1
covers: [L05, L09, L17]
requires: [cgo]
default_seconds: 60
max_workers: 8
---

# 场景

cgo 风暴长跑（需 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`，C 定义在 `storm.c`）。

每 worker 每轮：调用 C `c_storm_run`，C 侧创建 2–4 个 pthread 线程，每线程循环把 `//export goCb` 作为函数指针回调 Go。回调内：

1. 每次分配局部 buffer 并做内容校验（GC 时回调栈/寄存器必须可扫描）；
2. 约 1/23 的回调（由 `(t*31+i*17)%23==0` 确定）执行 24 层深递归，每层分配指针槽并在展开时校验；
3. 全局第 64 次回调处执行 `runtime.GC()`。

C 侧累计回调返回值并返回校验和与回调次数，父侧用纯函数 `cbValue/expectedStorm` 独立计算期望值比对。随后 Go 调用 C 阻塞函数 `c_block_ms`（nanosleep），验证 cgo 切换。`runtime.NumCgoCall()` 只记录不断言。

## 不变量

1. 每轮 Go 侧回调计数增量 = C 返回 `out_calls` = `nthreads*iters`；
2. C 侧累计校验和 = Go 独立计算的期望校验和；
3. 深递归每层 `local/pad[0]/pad[3]` 内容在 GC 后不变；
4. 回调内局部 buffer 内容不变（C→Go 回调帧可被 GC 扫描）；
5. `c_block_ms` 返回 0 或被信号打断（EINTR 只记录），调用不崩溃；
6. 心跳持续增长，watchdog 无停滞；
7. quiesce 后 goroutine 数不超过启动基线 + 8；
8. 结束打印 `STRESS PASS n07_cgo_storm`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 3，上限 8）
- `STRESS_HEARTBEAT`（默认 5s）
- `STRESS_FIXED_ROUNDS`（默认 8，仅 seconds=0）
- `UAF_SEED`

## 失败判读

- `phase=preflight: storm checksum=...`：`//export` 签名与 `storm.c` extern 声明不一致，或 C 侧未链接到 Go 回调；
- `phase=storm ... callbacks/out_calls=`：C 线程数/回调次数丢失，pthread join 或函数指针异常；
- `phase=storm ... checksum=`：回调参数/返回值被破坏（cgo 调用约定、寄存器保存或 GC 扫描问题）；
- `phase=callback-deep ... frame corrupted`：回调内深递归帧/指针槽在 GC 后损坏；
- `phase=callback ... local buffer corrupted`：回调帧局部变量被破坏；
- 构建失败：先确认 `CGO_ENABLED=1`、gcc 可用、`BUILD_MODE=module`；
- `watchdog stalled`：C 阻塞调用未释放 P 或回调路径死锁。

## 运行

```bash
CGO_ENABLED=1 SUITE=stress STRESS_SECONDS=60 PROFILES=soak FILTER=n07 ./run.sh
CGO_ENABLED=1 SUITE=stress STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 FILTER=n07 ./run.sh
# 手动构建（BUILD_MODE=module）：
cd stress/n07_cgo_storm && CGO_ENABLED=1 go build -o /tmp/n07 . && /tmp/n07
```

本场景不进默认 CI（`requires: [cgo]`）；`CAPS` 不含 `cgo` 时 runner 自动跳过。

## 来源

- `go/src/runtime/crash_cgo_test.go:62-120,170-206,315-344`（cgo 崩溃/回调路径）
- `go/src/runtime/testdata/testprogcgo/callback.go:51-114`（C 多线程回调 Go + Goexit/GC）
- `go/src/runtime/pinner_test.go:356-445`（回调/外部线程与 GC 交互）
