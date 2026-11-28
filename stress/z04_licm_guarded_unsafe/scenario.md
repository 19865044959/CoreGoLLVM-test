---
id: z04_licm_guarded_unsafe
priority: P0
covers: [L11, L17, L07]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

守卫内 `unsafe.Pointer` 算术被循环不变量提升（LICM）移出守卫的定向压力。每个 worker：

- 一个堆上 64KB `[]byte` buf，按 32B `nodeView{id,v,tag,next}` 视图逐格填充确定性 canary（2048 格）；
- 一组 512 个堆 `liveNode` 对象（同时经 `unsafe.Pointer` 视图访问）；
- 一个 `[]pairView`（uintptr 中间量）、一个 8KB pattern 字符串、一个混合 `interface{}` 数组（`*liveNode` 与 `string` 交错）。

每轮按 `(round+workerID)%5` 轮换、完整执行 5 种循环形态：

| 形态 | 代码骨架 | 被 LICM 盯上的不变量 |
|---|---|---|
| a 边界守卫 | `if i*viewSize < len(s) { p := (*nodeView)(unsafe.Add(unsafe.Pointer(&s[0]), i*viewSize)); ... }` | 指针基址 `&s[0]` 循环不变量；最后 16 次守卫为假 |
| b nil 守卫 | 循环外 `base` 按轮在 nil/`&nodes[0]` 间交替；`if base != nil { p := unsafe.Add(base, off) }`，`off` 为循环不变量 | nil 轮若算术被提升，`nil+off` 会跨安全点存活 |
| c 类型守卫 | `if v, ok := iface.(*liveNode); ok { p := (*liveNode)(unsafe.Add(unsafe.Pointer(v), 0)); ... }` | 仅在 ok 分支内做 unsafe 视图 |
| d uintptr 中间量 | `if j < len(arr) { u := uintptr(unsafe.Pointer(&arr[0])) + off; p := (*pairView)(unsafe.Pointer(u)); ... }` | 守卫内 uintptr 加法与回转 |
| e 字符串切片守卫 | `if j <= len(str) { p := unsafe.StringData(str); ... }`，最后 16 次守卫为假 | `unsafe.StringData` 循环不变量 |

每个守卫分支内先算出指针 `p`，再调用 `maybeGC`（每 `k` 次迭代显式 `runtime.GC()`，`k = 128 + (seed>>8)%256`，由 `UAF_SEED` 派生），最后读取并校验 canary——让指针跨 GC 安全点存活。所有解引用都在守卫成立、界内、对象存活的范围内；守卫为假时源码不计算越界指针（不引入真正的 UB）。轮末再抽查 buf 视图与 node canary。

独立 gcChurn goroutine 并发（`GOMAXPROCS=1` 时必须 `runtime.Gosched()+time.Sleep(1ms)`）。任何 canary 不符立即 FAIL；进程崩溃（`invalid pointer found on stack` / SIGSEGV）由 runner 记录日志、`STRESS_CRASH_FILE` 与 core。

# 对应历史案例

`docs/research/stress_history_bugs.md` 第 11 条：

- **go#82865 / go#83420（2026，Go 1.27 新回归）**：LICM 把守卫内的 `unsafe.Pointer` 算术提升出守卫，使本来只在“守卫成立”时才存在的无效指针在 safepoint 上保持存活，GC 报 `invalid pointer found on stack`。
- 覆盖缺口表第 4 行“守卫内指针算术被优化提升（LICM/边界守卫）”，此前 0 处专门形态。
- 关联维度：L11（编译期优化：LICM/SSA/边界检查消除）、L17（栈映射/安全点存活指针）、L07（unsafe.Pointer/uintptr/string 零拷贝）。

# 不变量

1. buf 的每一格 `nodeView`：`id==i`、`v==cellWant(seed,i)`、`tag==0x5A04`、`next==i+1`，所有显式 GC 前后一致；
2. 每个堆 `liveNode`：`id==i`、`v==nodeWant(seed,i)`、`tag==0x7A04`；nil/类型守卫只在守卫成立时解引用；
3. `pairView` 数组：`key==j`、`val==cellWant(seed,j)`；字符串每个读取字节等于 `byteWant(seed,j)`；
4. 每轮 5 种形态全部执行且顺序轮换；固定模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；
5. 每 worker `checks>0`、`started==finished`；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 末行 `STRESS PASS z04_licm_guarded_unsafe`。

# 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL 并附全栈 |
| `UAF_SEED` | 1 | seed；决定 canary、GC 间隔 k、形态轮换顺序 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

# 失败判读

- `view[...] corrupted` / `node[...] corrupted`：指针指向的堆内存被改写或对象被提前回收（UAF）；window/soak 下更敏感；
- `str[...]` / `arr[...] canary`：string/uintptr 形态读到错误内容；
- 进程崩溃且 stderr 含 `invalid pointer found on stack` / `bad pointer in frame` / `SIGSEGV`：守卫内的无效指针在 safepoint 上被 GC 扫描——即 go#82865/#83420 类回归；
- 只在 `window` 失败：STW/单 P 放大 LICM 提升后的存活性窗口；
- 只在 `-race` 失败：checkptr 报告指针算术越界（源码守卫失效或编译器重排）；
- `rounds=... want=...` / `started/finished` 不等 / `no rounds/checks`：worker 提前退出；
- `goroutine baseline ... leak`：收尾泄漏；
- `无 worker 进展`：卡死/GC 饥饿，附全栈。

# 运行

```bash
SUITE=stress FILTER=z04 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=z04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=z04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 BUILD_FLAGS=-race PROFILES=base REPEAT=1 TIMEOUT=300 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- go#82865 / go#83420（LICM 提升守卫内 unsafe.Pointer 算术）；
- `docs/research/stress_history_bugs.md` §1 第 11 条、§2 缺口表；
- `docs/research/stress_batch5_design.md` z04 条目；
- 现有相邻场景：u04（unsafe 零拷贝）、y07（typedmemmove+GC）、x13（safepoint 矩阵）——均无“守卫内指针算术被提升”形态。
