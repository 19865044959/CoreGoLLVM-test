---
id: u10_multi_gen_config
priority: P1
covers: [L02, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

三代配置并存热替换：配置代通过原子 pointer 发布，每代包含 `map[int64]*canaryNode`、`[]*canaryNode`、`interface` handler（handler 反向持有配置与 canary）和 finalizer probe。请求先 load 当前代并 `refs++`，再校验所持代的全部内容；换代由某个正在持有旧代引用的请求执行（完整构造新代 → `StorePointer` → 发布 `publishedGen` → 释放在“当前槽位”的引用 → 旧代进入 retired 列表）。因此换代瞬间必然存在“新代已发布、旧代仍被在途请求使用”的窗口：这些请求继续全量校验旧代，直到 `refs--`；retire goroutine 等所有 worker 都完成过换代后的请求（grace）且旧代 `refs==0` 后丢弃引用，由周期性 GC 回收。

周期换代 + GC 在长跑中反复覆盖“旧代对象在途时被换代、drain 后回收、分配器复用内存”的完整路径。任何旧代提前回收/漏标都会让下一次 magic 读取失配。

与相近场景的差异：

- s01 的 map/ring/interface 缓存只做容量淘汰，不做原子换代与引用计数回收；
- u01 打写屏障执行自身，node 始终被老对象图强引用；
- u10 专门打“原子 pointer 发布 + 旧代在途读取 + refs 归零回收”的生命周期，旧代内容在换代后仍被请求读取。

与 n04/u01 的互补关系：它们验证“改边后的存活性”，本场景验证“配置代对象在发布/退役过程中的可达性”。

# 不变量

1. 每次访问 `canaryNode` 都满足 `id/gen/magic == 期望`，`next` 指针与 `next` 的 magic 正确；map 条目与 order 切片指向同一对象；handler 的 `cfg/self/host` 反向引用正确、`Compute` 结果等于闭式公式、`Check` 无错；
2. `acquire` 先读 `publishedGen` 再 load 当前 pointer；load 到的代 gen 不得小于已读到的 `publishedGen`（原子发布只前进，不回退）；
3. 换代触发的那个请求在换代后仍持有旧代并做一次全量校验，因此 `old_gen_checks >= swaps`；
4. retire 只在 grace（所有 worker 均已结束或 `seenGen >= 旧代gen+1`）且 `refs==0` 后移除旧代；收尾要求 `retired_freed == retire_enqueued == swaps`，且每个旧代 drain 时 `refs==0`（残留即 FAIL）；
5. 收尾时当前代 `refs == 1`（仅槽位引用）、`inflight == 0`、`requests == refs_acquired`；
6. 当前代 probe 的 finalizer 从不执行（若执行说明当前配置在仍被原子 pointer 发布时被回收）；旧代 probe 可正常终结；
7. 固定轮数模式每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮、全局 ops/requests 与轮数×每轮请求数一致；`started == finished`；
8. 收尾后 goroutine 回基线（+4 容差）；
9. 最后一行 `STRESS PASS u10_multi_gen_config`。

# 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | worker 数（≤32） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | magic/随机种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 12 | `seconds=0` 时每 worker 轮数（每轮 8 个请求） |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

换代节奏：时间模式每 64 个请求尝试一次换代；固定轮数模式自动把间隔收紧到总 ops 的 1/4，保证小规模跑批也有多次换代。

# 失败判读

- `canary mismatch ... magic=... want=...`：节点已被回收/复用——旧代提前释放或 GC 漏标（重点看 `retired=true` 与 `phase=request`）；
- `next pointer broken` / `next canary corrupted`：配置内部的指针链被破坏；
- `entries[...] pointer mismatch`：map 与 slice 视图不一致（换代构造/写屏障问题）；
- `handler ...` / `Compute mismatch`：interface 装箱或 handler 反向引用损坏；
- `stale config gen=... published_gen=...`：原子 pointer 发布回退（发布顺序/可见性错误）；
- `current config ... finalized while still published`：当前代在仍被原子 pointer 引用时被回收；
- `retired gen=... still has refs=... after all workers finished`：引用计数泄漏/释放遗漏；
- `goroutine leak`：收尾未 quiesce；
- 只在 soak/clobberfree 下失败：UAF/漏标；base 也失败多为校验公式或场景逻辑错误。

# 运行

```bash
SUITE=stress FILTER=u10 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 PROFILES=base ./run.sh
```
