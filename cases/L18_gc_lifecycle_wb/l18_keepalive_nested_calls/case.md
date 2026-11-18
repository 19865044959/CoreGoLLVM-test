---
id: l18_keepalive_nested_calls
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:10072（TestNoRaceConnectionObjectReleased：finalizer + GC 轮询验证对象释放）
  - badger@v4.5.0: batch_test.go:157（TestBatchErrDeadlock：错误路径后释放/再次使用对象，覆盖生命周期边界）
tags: [L18, KeepAlive, finalizer, noinline-call-chain, defer, interface, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

用 finalizer 作为存活探针，覆盖三种 `runtime.KeepAlive` 边界，对象只在边界函数内部创建：

1. 调用链边界：noinline `knNestedOnce` 创建 3 个对象，经 `knLevel2→knLevel3→knUse` 读取字段后进入 3–5 轮 GC + finalizer 窗口，窗口末尾读取原子标记（必须为 0），再对 3 个对象执行 `runtime.KeepAlive`；
2. interface 边界：对象仅以 `interface{}` 持有，类型断言读取后进入 GC 窗口，标记检查后 `runtime.KeepAlive(iface)`；
3. defer 边界：`defer runtime.KeepAlive(o)` 注册后使用对象，GC 窗口内标记必须为 0，函数返回时 defer 才执行。

三种边界各重复 5 次，每次 3 个对象。函数返回后对象失活，主协程轮询至全部 finalizer 触发。

# 期望行为

1. KeepAlive 边界前（含多轮 GC 与 finalizer 窗口）所有对象标记为 0；
2. 三种边界（调用链、interface、defer）均成立；
3. 边界返回后所有对象在 5s 内最终被终结；
4. 结果不依赖 finalizer 具体调度时刻；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_keepalive_nested_calls`。

# 失败判读

- `finalized before KeepAlive boundary`：编译器把最后一次字段使用后的指针提前判死，KeepAlive 未生效；
- `not finalized after boundary returned`：对象被意外保活或 finalizer 未注册；
- 只断言“可达不终结 / 失活最终终结”，不读 finalizer 中的 payload。

# 参数

- `UAF_SEED`：GC 窗口轮数（3–5）与数据种子；
- 无 TORTURE 参数。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_keepalive_nested_calls/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
