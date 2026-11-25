---
id: u04_unsafe_zerocopy_buffer
priority: P0
covers: [L07, L17, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

unsafe 零拷贝缓冲长跑：worker 在四种持针形态下把 `[]byte` 的底层存储交给派生视图，并让**派生视图成为唯一引用**跨 GC，用确定性 canary 让 UAF/漏扫/header 拆装错误当场现形。

每 worker 每轮（缓冲 6–10KB，阶段 2 放大 3 倍）：

1. **零拷贝 string 视图**：noinline 构造 `b := make([]byte, n)` 并填充 `pat(seed,worker,round,off)`，`*(*string)(unsafe.Pointer(&b))` 单表达式转换后返回；base 丢弃，仅 string 保活。GC 后校验 `len`、FNV 校验和、`unsafe.StringData` 派生的只读字节视图逐项一致。
2. **slice header 拆装**：`*(*sliceHeader)(unsafe.Pointer(&b))` 提取 `data/len/cap`；GC 后由 header 值 `*(*[]byte)(unsafe.Pointer(&h))` 重装并校验；再从 `unsafe.Add(h.data, off)` 派生 `len/cap` 收窄的子视图，`sub[:len/2]` 后 `[:cap]` 恢复全宽，逐视图校验 offset 对应的 pat 序列。base slice 在 GC 前丢弃。
3. **内部指针唯一引用**：noinline 返回 `&b[off]`（off=n/3+1）与尾部元数据；base 丢弃，仅内部指针存活。GC 后用 `unsafe.Slice(p, tail)` 重建尾部视图，校验校验和与首/尾字节。
4. **多视图别名一致性**：同一缓冲上同时派生 string / 子 slice / header（并核对 `StringData == &buf[0]`），GC 前校验一致，丢弃 base 再 GC 后三视图仍读到同一 pat 序列。
5. 每个 worker 另持一组**长生命周期视图**（string + 内部指针），每轮与收尾（两轮 GC 后）校验，防止长活视图被提前回收。

独立 gcChurn 持续分配 4KB 对象 + 显式 GC，与所有校验并发，扩大回收/复用窗口。两个阶段按固定轮数或运行时长均分；阶段 2 缓冲放大 3 倍。

来源：`go/src/unsafe`（String/Slice/StringData/SliceData）、`cases/L07_unsafe_zerocopy/l07_readonly_string_header_view`、`l07_readonly_window_alias_scan` 的 header/内部指针思路，放大为跨 GC 长跑。

## 不变量

1. 零拷贝 string 视图在 base 不可达、显式 GC 后 `len` 与 FNV 校验和不变；`unsafe.StringData` 非 nil 且内容一致；任何字节不匹配立即 FAIL；
2. header 拆装的 `data/len/cap` 精确等于原 slice；`unsafe.Add` 派生 offset、cap 收窄与 `[:cap]` 恢复的视图内容与 pat 序列逐字节一致；
3. 内部指针作为唯一引用跨 GC 后，`unsafe.Slice` 重建的尾部视图校验和/首尾字节正确（内部指针保活整块对象）；
4. 同一缓冲的 string/子 slice/header 三视图在同一 pat 序列上别名一致，GC 前后均成立；
5. 每个 worker 的长生命周期视图在每轮与收尾校验中保持完好；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started == finished == workers × phases`；
7. 收尾 quiesce 后 goroutine 回基线（+8 容差）；`alias_views/header_ops/interior_probes` 非零；
8. 最后一行 `STRESS PASS u04_unsafe_zerocopy_buffer`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 6 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL + 全栈 |
| `UAF_SEED` | 1 | pat/magic 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 24 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `alias-post-gc: checksum=... want=...` / `view checksum`：string 视图未保活底层数组或 header 重装错误——先看 `GODEBUG=clobberfree=1` 是否更早出现；soak/soak-single 下失败基本可判 UAF/漏标；
- `header-sub: cap=... want=...` / `view len=...`：len/cap 拆装或 `unsafe.Add` 偏移计算错误；
- `interior: checksum=... want=...`：内部指针未保活整块对象（GC 后内容被回收/复用）；
- `alias-identity: string data != &buf[0]`：零拷贝转换得到的是副本而非别名；
- `live-string` / `live-internal-pointer`：长生命周期视图被提前回收；
- `no worker progress`：watchdog 超时（校验或 GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- `zero coverage`：场景自身未推进（仅收尾检查）。

## 运行

```bash
SUITE=stress FILTER=u04 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u04 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
