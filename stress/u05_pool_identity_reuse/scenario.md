---
id: u05_pool_identity_reuse
priority: P0
covers: [L05, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

sync.Pool 对象身份复用与回收竞态长跑：池中对象是**含大量指针字段的大对象**（40KB data 区 + 16 个直接指针 + 8 项 slice 指针 + 8 项 map + interface 装箱 + 24 个标量），用 `owner/gen` 双维 canary 让任何旧指针残留、字段未重填、内容漂移当场现形。

每 worker 每轮：

1. **Get → 预校验 → 重新填充 → Put → 再 Get**：Get 到的复用对象先按对象自带的 `owner/gen` 重算上一代全部 canary（跨 worker 复用同样可校验）；随后全字段重填（data 逐字节 pat、指针全部换新、slice 原地复用并整体覆盖、map clear 后重插、interface 按 gen 奇偶装箱 `*pointee`/`[]*pointee`）；校验 map 长度恰为 8（无旧 key）、boxed 类型与奇偶一致、所有指针 identity 与内容匹配；Put 后立即再 Get 并重复一轮。
2. **阶段 1 额外 GC**：取得对象后显式 GC（持有跨 GC），Put 后显式 GC 再 Get（覆盖 victim/清池后复用或 New 两条路径）。
3. **外部引用后 Put**（每 5 轮）：对象填充后 Put 回**worker 私有池**，外部引用仍持有；GC 后外部读校验内容完好；再从池取回校验/重填/归还。
4. **victim 两级缓存**（每 8 轮）：向 worker 私有池 Put 两个对象后连续两轮 GC（私有池无并发访问），随后 Get 必须走 `New` 路径（`New` 调用计数 +1，且返回对象 `gen==0`、全零/全空）；再把新对象填充/校验/归还。
5. 独立 gcChurn 持续分配 + GC，与所有 Get/Put 并发。

两个阶段按固定轮数或运行时长均分；阶段 1 开启 Put 前后 GC。`reuse_hits` 只记录不断言，身份复用正确性由每次预校验/重填后校验保证。

## 不变量

1. 从池中取出的对象内容要么是新对象（`gen==0` 且指针/切片/map/raw/boxed 全零或空），要么完整匹配其 `owner/gen` 的确定性 canary；任何指针字段为 nil、magic 不符、map 长度不为 8、boxed 类型/identity 不符立即 FAIL；
2. 复用对象重填后，16 个直接指针、8 个 slice 指针、8 个 map 值、interface 装箱全部指向本代新对象，data 区逐字节 pat 一致，raw 字段按公式一致——旧指针残留即失配；
3. 外部引用在 Put 回私有池并 GC 后仍读到完好内容（池拿回不等于对象失效）；
4. 私有池经两轮 GC 后 victim 两级缓存被清空，Get 必然调用 `New` 且返回全新零值对象；
5. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮，`started == finished == workers × phases`；
6. 收尾 quiesce 后 goroutine 回基线（+8 容差）；`gets/puts/obj_checks/ext_checks/victim_new_path` 非零；
7. 最后一行 `STRESS PASS u05_pool_identity_reuse`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 6 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL + 全栈 |
| `UAF_SEED` | 1 | canary 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

## 失败判读

- `get-pre: ptrs[...] corrupted` / `data checksum`：池中对象在 Put 与 Get 之间被改写/提前回收（UAF）——clobberfree/soak 下优先怀疑；
- `map len=... want=8` / `map[kN] missing`：map 未清空或重插不完整，存在旧 key 残留；
- `boxed type ... (旧 interface 残留)` / `boxed identity !=`：interface 装箱槽未被新代覆盖；
- `fresh ... want 0`：New 路径返回的对象带有旧内容（分配未零化/池错误返回脏对象）；
- `ext-after-put-gc: ...`：对象归还池后虽被池拿回，外部引用仍应有效——失配说明对象被回收/复用；
- `victim 缓存两轮 GC 后仍存活` / `reused object survived two GCs`：victim 两级缓存老化语义错误；
- `no worker progress`：watchdog 超时（填充/校验或 GC 卡死），附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏。

## 运行

```bash
SUITE=stress FILTER=u05 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
