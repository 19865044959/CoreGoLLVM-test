---
id: y07_typedmemmove_gc
priority: P0
covers: [L18, L17]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

C17 GoTypedMemmove 穷举——含指针字段大值（`Big` 264B：16 指针 + 16 words + seq）的全部批量搬运路径 × GC 落点穷举（搬运前/搬运中/搬运后）。n04 只打 `[]*node` 指针批量，u01 打字段写；本场景打**含指针的大值搬运**（typedmemmove）：

- **copy(dst, src)**（dst/src 为 `[]Big`，非 `[]*T`）：
  - 搬运前 GC（零值 dst 被扫）→ copy → src 丢弃 → 搬运后 GC（搬运后指针只经 dst 可达，必须被标记）；
  - 拆分两段 copy，段间 GC（半写 dst 被扫描——搬运中间落点）；
  - 脏 dst（预填指向短命节点的大值）+ 写前 GC + **并发 GC 与 copy 写屏障重叠**（u01 风格）+ 搬运后 GC；
- **append 扩容搬运**：空容量逐步 append 触发增长 memmove（老元素）+ 增长中间 GC + 批量 `append(dst, more...)`；
- **map 值拷贝**：`m[k] = bigV`（桶内值拷贝）+ 64 项插入触发增长 evacuation（值 typedmemmove）+ 插入中间 GC + delete/重插 + 全键读回校验；
- **chan send/recv 大值**：buffered chan 值拷入 buffer → 写后 GC（值只经 buffer 可达）→ 半读 GC → recv 校验；
- **interface 装箱大值**：`boxes[i] = mkBig(...)` 堆拷贝 → 装箱后 GC → 半拆箱 GC → 拆箱校验；
- **自搬运 copy(a, a) 重叠区**：右移/左移，段间 GC，按置换后期望逐字段核对（memmove 重叠语义）；
- **dst 在栈（addrtaken）与堆两种**：`[2]Big` 栈局部（defer 闭包 + noinline 非保留调用取址），copy 写栈 dst（GoRemoveStackWriteBarriers 路径），写前 GC 扫未初始化槽（脏字节窗口）；
- **源老年代 → 目标年轻/年老**：src 两轮 GC 晋升后搬运（写屏障窗口，并发 GC 重叠）+ old→old 对照；
- 每轮 poison 同 size class 填埋；搬运后逐字段 canary 校验。

差异说明：与 n04/u01 不同，这里每个元素是 264B 含 16 指针的聚合值——typedmemmove 一次搬运 264B 且内含写屏障位图。官方 Go 恒 PASS；漏写屏障/漏标仅随 gollvm 出现（C17）。

来源：`docs/research/stress_batch4_design.md` y07；`docs/research/stress_extreme_survey.md` C17/x12；`gp/GoTypedMemmove.cpp`、`GoRemoveStackWriteBarriers.cpp`；n04/u01 手法。

## 不变量

1. 每种搬运路径搬运后（含 src 丢弃后的 GC）dst 的每个 Big 节点指针/words/seq 与 (seed, base) 期望一致；
2. 拆分/中间 GC 后前段与后段各自完整；并发 GC 重叠下搬运结果一致；
3. map 全键（含 delete/重插键）读回值与期望一致；chan 顺序 recv 与发送序一致；拆箱值与装箱源一致；
4. 自搬运右移后 `a[i]=orig[i-1]`、左移后 `a[i]=orig[i]`（i<n-1）且 `a[n-1]=orig[n-2]`；
5. 栈 dst（addrtaken）写前/写后 GC 后 defer 校验一致；old→young 与 old→old 均完整；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；worker `started==finished`；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；`Big` 尺寸恒 264B；
8. 最后一行 `STRESS PASS y07_typedmemmove_gc`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | seed，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `canary ... (UAF/typedmemmove 漏标)` / `nil *node`：搬运后指针未被写屏障记录/标记，src 丢弃后节点被回收——本场景核心信号，最敏感于 `clobberfree`/`window`；
- `搬运后内容错位/复用`：typedmemmove 位图/长度错误（源/目标重叠、跨元素串位）；
- `words/seq ... (搬运后内容被改写)`：copy 重叠区处理错误（自搬运/批量 append 增长路径）；
- `len=%d want=%d`：append 增长语义错误；
- `key=%d 缺失`：map 值拷贝/evacuation 丢数据；
- `拆箱类型=...`：装箱/拆箱类型信息损坏；
- `6 个心跳周期无进展`：卡死（附全栈）；`goroutines baseline ... leak`：收尾泄漏；
- 只在 `detector`（efence）崩溃：释放对象被再次访问（UAF）。

## 运行

```bash
SUITE=stress FILTER=y07 STRESS_SECONDS=3 PROFILES=soak,window REPEAT=1 TIMEOUT=120 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
SUITE=stress FILTER=y07 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base,clobberfree REPEAT=1 TIMEOUT=180 GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ./run.sh
```

## 来源

- `docs/research/stress_batch4_design.md` y07（C17 从未打）；`docs/research/stress_extreme_survey.md` C17/x12；
- `gp/GoTypedMemmove.cpp`、`gp/GoRemoveStackWriteBarriers.cpp`、`rt/gc_test.go`（typedmemmove 测试）；
- `stress/n04_write_barrier_graph`（指针批量）、`stress/u01_wb_boundary_storm`（并发 GC 重叠手法）。
