---
id: y08_global_gcprog_storm
priority: P1
covers: [L18, L13]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

全局巨型 gcprog 风暴：把 **B14/B15（issue1002 路径）**——巨型全局数组的指针位图 gcprog 编码/解码/缓存——逼到边界。编码、解码或缓存任一环节出错：全局根扫描**漏标**（对象被提前回收 → canary 失配）或**多标**（pad 非指针字被当指针扫 → GC 直接崩 "bad pointer"）。

全局形态（总量 ≤256MB、总指针位 ≥100 万，preflight 断言；生成器脚本保证预算）：

- **核心全局（手写）**：
  - `gCorePtr [1<<16]*node`（65536 个指针位）；
  - `gCoreKv [1<<14]Kv`（`k string / v *node / s []byte / e any` 混合，5 指针位/槽）；
  - `gCoreChan chan [512]Kv`（chan 元素为 512×Kv 大数组，容量 8）；
  - `gCoreMap map[string][64]*node`（map 值 64 指针数组，mutex 保护）；
  - `gCoreInner [1<<12]Inner`（`Mid`/`Deep` 嵌套 struct 数组，28 指针位/元素、多层指针位）；
- **生成全局**：`tools/gen_global_types.py` 生成 36 种不同布局的巨型全局数组（gofmt 后入库，布局参数是类型下标的纯函数——确定性可重跑、sha 一致）。偶数下标 = 稀疏布局（大 pad run，推入 gcprog 压缩形态）；奇数下标 = 密集布局（大数组、小 pad，原始位图形态）。

运行时（每轮）：

1. worker 按 `STRESS_SCALE` 触达面（small=1 全局/轮、medium=2、full=4）轮转访问全局，**分区改写**（worker 各占 1/N 槽，互不重叠）：老→新指针、nil↔对象交替、Kv 槽全零↔混合内容、chan 往返（`[512]Kv` 全量 canary）、map 增删+周期回环校验、生成数组 pad 字写入**高位非规范模式**（0xDEAD…，位图多标当场崩）；
2. **每轮 GC**：写入 → `runtime.GC()` → 本轮区域精确复核（漏标 → canary 失配）；region 回环（tag≥regions）时写前回环校验上一周期内容；
3. `reflect.PointerTo/ArrayOf/SliceOf` 大量并发构造打 **typelinks**（多 worker 并发 + 每轮 GC 交错），生成类型的 size/ptrdata 与生成器元数据确定性对照（经 unsafe 读运行时 `_type` 的 PtrBytes 字段），周期 `reflect.New` 物化 gcdata（gcprog 解码路径）。

独立 churn goroutine 持续 `runtime.GC()`（GOMAXPROCS=1 时 Gosched+1ms sleep，防 window 饿死）。

## 差异

与 s05（64 个小类型生成代码）不同：本场景全部是**巨型全局对象**，位图要么跨多个 word 要么超长压缩，直指 issue1002 的 gcprog 路径；与 u01（堆对象字段写屏障）不同：全局根每轮 GC 全量扫描，不依赖写屏障——只依赖全局位图与 typelinks 的正确性。设计溯源为 x11（survey 中"设计了未实现"），本批落地。

## 不变量

1. 每个 node 的 `v == nodeMagic(seed,id)`；任何读出的指针内容与确定性写入序列精确一致（nil 槽必须 nil、非 nil 槽 id 精确匹配）——写入 → GC → 复核 + 回环校验两段全覆盖；
2. 生成数组 pad 字恒等于 `padWord(seed,tag,j)`（非规范高位模式），被当指针扫即崩溃；
3. chan 往返的 512×Kv 全量 canary 精确校验（固定 chanSeed，任何 worker 都能校验任何发送者）；
4. map 值 64 指针数组在增删+多轮 GC 后精确校验；
5. reflect 构造的类型 size/ptrdata 与生成器元数据一致；核心类型（含 chan 元素 `[512]Kv`）尺寸正确；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；`started==finished`；
7. 收尾 quiesce 后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS y08_global_gcprog_storm`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤16；各占全局 1/N 槽分区） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 随机/ID 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 40 | `seconds=0` 时每 worker 轮数 |
| `STRESS_SCALE` | small | `small|medium|full`：每轮访问全局数（1/2/4）、写入 stride（64/512/4096 槽）、reflect 构造数（4/16/32）；全局总量固定，scale 只调触达面（自测用 small） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径（多标崩溃文本在此） |

## 失败判读

- `canary mismatch ...（漏标/复用）`：全局根扫描漏标 → 对象被回收/复用——gcprog 解码错误（重点看 `phase=y08.gen.*.cycle|post` 与字段名；`soak`/`clobberfree` 下最敏感）；
- `pad[...] want=...（全局被改写/位图错位）`：位图覆盖错位（gcprog 编码错位）；
- `fatal error: found bad pointer` / 崩溃文件出现 `0xdead...` 地址：**多标**——pad 字被当指针扫（`STRESS_CRASH_FILE` 捕获）；
- `size/ptrdata ... want ...（类型元数据损坏）`：typelinks/类型元数据在并发构造下损坏；
- `reflect ... 损坏`：PointerTo/ArrayOf/SliceOf 构造结果错误；
- `初始应为 nil`：全局零初始化/回环定位错误；
- `chan ... canary/损坏`：chan 元素位图漏标；
- `map: key 缺失` / map canary：map 值数组漏标；
- `no worker progress`（watchdog）：GC/typelinks 卡死，附全栈；
- `goroutines baseline ... leak`：收尾 goroutine 泄漏；
- 只在 `window`（gccheckmark）失败：漏标；`clobberfree` 下 canary 失配：UAF/复用。

## 运行

```bash
# 生成器（确定性，可重跑 sha 一致）：
python3 tools/gen_global_types.py

SUITE=stress FILTER=y08 STRESS_SECONDS=60 PROFILES=soak,window ./run.sh
SUITE=stress FILTER=y08 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=40 PROFILES=base,clobberfree STRESS_SCALE=small ./run.sh
SUITE=stress FILTER=y08 STRESS_SECONDS=30 STRESS_SCALE=full PROFILES=layout ./run.sh
```

## 来源

- 调研文档 `stress_extreme_survey.md`：B14（全局 gcprog）、B15（gcprog 缓存）、x11 global_gcprog 条目（issue1002 路径）；
- go#1002（全局 gcprog 编码错误）；`gollvm/gofrontend/go/types.cc:3432-3443`（类型位图/gcprog 生成）、`gp/Util.cpp:400-430`；
- `go/src/runtime/gc_test.go`（全局大对象扫描）、`go/src/runtime/testprog/typelinksrace.go`（typelinks 并发构造竞态）、`internal/abi.Type`（Size_/PtrBytes 布局）；
- harness 复用 u01（nodeMagic/canary 三段校验）与 n04（gcChurn GOMAXPROCS=1 让出）。
