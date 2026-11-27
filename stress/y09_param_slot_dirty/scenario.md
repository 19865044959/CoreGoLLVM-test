---
id: y09_param_slot_dirty
priority: P1
covers: [L17, L11]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

参数槽不初始化（A8）：参数槽（内联后成为调用方局部槽 / defer 参数求值槽 / 溢出到栈传参区）未正确进栈图时，GC 要么扫到脏值（悬挂指针 → 崩溃）要么漏扫（对象被回收 → canary 失配）。

三个形态（每轮全做）：

- **形态 A（内联参数槽）**：可内联小函数 `useParam(seed, p, want)` 内联进调用方 → 参数成为**调用方局部槽**。调用前该栈位是脏值：`dirtyRec` 以深度抖动（d = 16 + round%48）递归把各层帧刷满指向短命堆对象的指针、depth 0 GC 清扫成悬挂，`victimInlineRec` 同型同深度下降（帧尺寸一致）使底部帧槽位落在旧 dirtyRec 帧的脏字节上（同 y01/g01 手法）。随后**取址导出**（`&p` 存入局部不逃逸 chan）→ GC → 经导出指针读回校验；另有**槽复用变体**（同槽换新对象，再 GC 再校验——槽位图跨两代对象）。编译器不内联时此形态退化为 callee 参数区覆盖（同样命中 A8）；
- **形态 B（defer 参数求值时点）**：`defer useParamDefer(seed, p, want, ch)`——参数在 **defer 语句时求值**、defer 执行时使用，窗口内两次 `runtime.GC()`（求值后一次、defer 执行时一次）；先执行的 defer 只 GC+分配（不引用 p，防闭包兜底保活）。B1 = 开放编码 defer（栈槽）；B2 = 循环内 defer（堆 `_defer` 记录参数区），两变体按轮次交替，XOR 累积做顺序无关的整体校验；
- **形态 C（多参数混合，8+ 指针溢出栈传参区）**：noinline callee 接收 12 指针 + int/string/slice/iface/float 混合参数——ABIInternal 下 a1..a9 占满 9 个整型寄存器，**a10 起溢出到栈传参区**（a9 末寄存器、a10 首栈槽，args 区边界）；callee 内取址导出 `&a10` + GC + 全参数精确校验；调用方经脏栈铺垫下降后把 carrier 值作实参（outgoing args 区同样落在脏字上）。

独立 churn goroutine 持续 `runtime.GC()`（GOMAXPROCS=1 时 Gosched+1ms sleep，防 window 饿死）；每轮 weak 强引用探针。

## 差异

g 系列打的是 **goret 结果槽**未初始化（命名结果/晚写入），本场景打**参数槽**三族：内联参数局部槽、defer 参数求值槽、栈传参区边界。g01 的脏栈制造（dirtyRec 同型下降）只复用骨架；不重复其 goret/接口闭包晚写入形态。x09 只测 asm 帧的 args stackmap，本场景是纯 Go 内联/defer/寄存器溢出路径。不在 harness 内直接读取已回收的栈内存（未定义行为，stock 也可能崩）；判据一律走合法引用（导出指针槽 / defer 执行 / 全参数校验）或 canary。

## 不变量

1. 形态 A：经导出指针读回的对象 canary 精确一致（导出前 GC 后、槽复用后各一次）；
2. 形态 B：每个 deferred call 的校验和与 (seed, base+i·10) 派生值一致（XOR 累积整体校验）；defer 执行时点再 GC 后 canary 仍完整；
3. 形态 C：a10 经导出槽读回 canary 一致；12 指针参数、int/string/slice/iface/float 全部精确匹配（args 区边界两边：a9 寄存器、a10 栈槽）；
4. 每轮 weak 强引用探针：GC 后 `weak.Value() != nil` 且 canary 一致（漏标 → 提前回收）；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started==finished`；形态计数各 == rounds；
6. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
7. 最后一行 `STRESS PASS y09_param_slot_dirty`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 随机/ID 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 100 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `nil payload ... 参数槽漏标/UAF`：参数槽未被栈图覆盖 → 对象被回收（`soak`/`clobberfree` 下最敏感；`window` 的 gccheckmark 直接判未标记可达）；
- `canary mismatch`：槽被扫到脏值/复用对象（参数槽错标为其他地址，或扫描了悬挂指针）；
- `校验和 ... want ...（defer 参数槽漏标）`：defer 参数求值→执行窗口内槽漏标；
- `a10-via-slot / many.arg`：栈传参区边界（寄存器↔栈）的栈图缺失或错位；
- `many: s/b/e/x/f` 失配：栈传参区非指针参数被改写（栈图错位）；
- `weak ... strong ref held but object collected`：本地强引用下对象被回收（漏标）；
- 只在 `window` 失败：漏标/窗口；`detector` 崩溃：释放对象被再次访问（UAF）；
- `no worker progress`（watchdog）/ `goroutines ... leak`：卡死或泄漏，附全栈。

## 运行

```bash
SUITE=stress FILTER=y09 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree ./run.sh
SUITE=stress FILTER=y09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=5 BUILD_FLAGS=-race PROFILES=base ./run.sh
```

## 来源

- 调研文档 `stress_extreme_survey.md`：A8（args/locals 栈图）、A11（defer）、x01/x09 的 args 区边界条目；
- g01/g03 的 dirtyRec 同型下降手法（issue2 §2.1，本场景复用骨架不重复形态）；
- `go/src/cmd/compile/internal/ssagen`（ABIInternal 参数分配：9 整型寄存器 → 栈溢出）、`abi`（参数分类）；
- harness 复用 x05（failf/watchdog/weak 探针）与 n04（gcChurn GOMAXPROCS=1 让出）。
