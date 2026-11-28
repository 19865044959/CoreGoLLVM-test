---
id: z08_escape_heap_to_stack
priority: P0
covers: [L17, L18, L05]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

逃逸分析失败面：**堆持有栈指针**（go#10353 / go#79592）。每个 worker 每轮轮换 5 种“本该逃逸到堆”的形态；若编译器漏判逃逸，全局存储里的指针就指向已返回的栈帧，GC 后读取即 UAF。

每轮构造（辅助函数均 `//go:noinline`，其栈帧在 GC 前必然已返回）：

- **a) 闭包捕获局部变量**：`x` 按引用捕获（`moved to heap: x`），闭包逃逸到全局 `chan func() int64` 与全局 `interface{}` 槽；GC 后分别经 chan 与 interface 调用，校验两次递增后的捕获值；
- **b) 局部数组 + 就地 append**：`var a [16]*node; s := a[:8]`，再 `append` 8 个元素（cap 足够，不重分配，`&s[0]==&a[0]`），把 `s` 存入全局 `map[string][]*node`；GC 后读回全部 16 个元素；
- **c) interface{} 装箱局部指针**：返回装箱指向局部 node 的 `interface{}`，存入全局 chan；GC 后类型断言并校验字段；
- **d) 值接收者方法值**：`m := r.score` 捕获 receiver 副本（副本内含 `*node`），存入全局 chan；GC 后调用并校验结果；
- **e) 泛型取参数地址返回**：`returnLocal[T](v T) *T { return &v }`，实例化 `int64` / 结构体 / `[2]int64`，三个返回指针存入全局 chan；GC 后逐个类型断言、读回字段（含结构体内的 `*node`）。

探针：

1. 每个逃逸 node 注册 finalizer（仍被全局/本地强引用而终结 = FAIL）；
2. `weak.Make`：强持有期间 `Value()` 必须非 nil；
3. node 内容为确定性 canary（`nodeMagic(seed,id)`），任何读取不匹配立即 FAIL。

每轮在“逃逸存储后、读取前”插入 `runtime.GC()` 窗口，并先调用 `stackChurn`（深递归 + 指针槽）复用逃逸源函数的栈帧；读取后再做第二次 GC 复查。独立 goroutine 并行提供 GC churn。上一轮探针先 `SetFinalizer(nil)` 再释放，避免正常终结污染断言。`window`（`GOGC=off GOMAXPROCS=1 gcstoptheworld=2,gccheckmark=1`）下必须通过。

## 对应历史案例

- **go#10353（2015）**：逃逸分析错误——闭包捕获局部变量后闭包逃逸到堆，堆持有栈指针 → 栈帧回收后 UAF。对应形态 a（以及 d/e 的“捕获副本”变体）。
- **go#79592（2024）**：泛型实现缺逃逸分析（反向的“过度不逃逸/忽略逃逸约束”即 UAF）。对应形态 e 的多类型实例化。
- 补充覆盖 stress_history_bugs.md §2“逃逸分析失败（堆持有栈指针）”缺口，与 y03（泛型返回值槽）互补：y03 打返回值槽内容，本场景打“局部对象错误留在栈上”。

## 不变量

1. 五种形态的逃逸对象在 GC（含“存储后、读取前”窗口）后内容仍为确定性 canary；
2. 闭包捕获的标量经 chan 与 interface 两次调用返回值精确为 `base+1`/`base+2`；
3. 局部数组 slice GC 后 `len==cap==16` 且 16 个元素全部正确（栈数组未被复用）；
4. 方法值 receiver 副本调用结果、泛型三种实例化的参数副本内容与内嵌指针均正确；
5. 强持有期间所有 `weak.Value()!=nil`，且没有任何可达逃逸对象被终结（finalizer 计数恒 0）；
6. 每轮 2 次显式 GC；固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started==finished`；
7. 收尾 goroutine 回基线（+4 容差）；最后一行 `STRESS PASS z08_escape_heap_to_stack`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子（canary 与失败输出） |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `closure: captured value=... want=...`：闭包捕获变量所在栈帧被复用 → 逃逸分析漏判（go#10353 形态）；
- `append[...] canary mismatch` / `nil *node`：slice 底层局部数组被复用 → 数组未逃逸；
- `iface: type assertion failed` / `canary mismatch`：interface 装箱指针指向栈；
- `method: receiver copy result=... want=...`：方法值捕获的 receiver 副本留在栈上；
- `generic.*: value/content corrupted`：`&v` 指向栈帧（go#79592 形态）；
- `weak[i]: Value()==nil while strongly referenced` / `reachable escaped object was finalized`：对象被提前回收（UAF）；
- `append-build: append reallocated`：场景自身构造退化（非被测目标）；
- `phase=watchdog`：GC 窗口卡死，附全栈；
- 只在 clobberfree/soak 下失败：典型 UAF；base 也失败多为逃逸形态构造错误。

## 运行

```bash
SUITE=stress FILTER=z08 STRESS_SECONDS=60 PROFILES=soak,window,soak-single ./run.sh
SUITE=stress FILTER=z08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=z08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base ./run.sh
```

## 来源

- [go#10353 closure escape](https://go.googlesource.com/go/+/878a86a129ceb771f677c8391a9f5c891e5be7c3)
- [go#79592 generics escape](https://github.com/golang/go/issues/79592)
- `stress/x14_weak_resurrect`（finalizer/weak 探针写法）、`stress/y03_generic_ret_slot`（泛型返回）
