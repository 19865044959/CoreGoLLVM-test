---
id: u12_map_grow_stress
priority: P0
covers: [L13, L18]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

单 map 扩容/搬迁长跑：唯一的 `map[int64]*mapNode` 从 0 持续增长到 `STRESS_MAP_MAX`（默认 1<<20），多次触发内部扩容/搬迁；后半程逐 key 删除（改变 bucket 状态），删除完成后按每个 64 key 重插 1 个（新 gen=2 节点）。全程有多个 reader 并发持有 `RWMutex` 读锁做：

- 随机 key 存在性探测：按当前 phase/frontier 判定期望；present 时校验 key/gen/magic canary；
- frontier 边界探测：未插入、已删除、未重插的 key 必须缺失；插入/删除/重插前沿必须存在；
- `range` 遍历抽样（每轮最多 2048 个 entry）：迭代期间看到任何模型外 key 或 canary 失配立即 FAIL。

writer 每次只在写锁内做一小块（chunk），使 reader 的遍历与各次扩容/搬迁交错发生；phase 与 frontier 都在写锁内推进，reader 在读锁内取得一致快照。节点只有 24 字节（key/magic/gen），控制百万级 entry 的内存。

与 n02 的差异：n02 是分片 map 的**稳态翻腾**（写锁读写抽查、遍历中删除/重插、sync.Map、NaN key 边界）；u12 是**单 map 的完整生命周期**（0→百万级增长、多次扩容搬迁、再全量删除、少量重插），专打扩容搬迁路径的长跑。

# 不变量

1. 每个被访问节点满足 `key` 与 map key 一致、`gen ∈ {1,2}`、`magic == mapMagic(seed,key,gen)`；grow/delete 阶段只允许 gen=1，reinsert/done 阶段只允许 gen=2；任何失配立即 FAIL；
2. 随机/边界探测的 present/absent 必须与 phase + frontier（`inserted/deleted/reinserted`）推导的模型一致；
3. 迭代中出现的每个 key 都必须属于当前模型（扩容搬迁后的 stale entry/旧 bucket 残留会违反）；
4. grow 完成时全量校验 `len(map)==max`；delete 完成时 `len(map)==0`；reinsert 完成与收尾时 `len(map)==ceil(max/64)` 且逐项 canary 正确；
5. 固定轮数模式 writer 恰好 `STRESS_FIXED_ROUNDS` 轮；reader 持续抽样到 writer 完成全部阶段并至少完成 1 轮；`started == finished == workers+1`；
6. 时间模式按 elapsed 60%/85% 推进 grow/delete/reinsert，收尾强制补齐，最终必须处于 done 且 frontier 完整；
7. 收尾后 goroutine 回基线（+4 容差）；
8. 最后一行 `STRESS PASS u12_map_grow_stress`。

# 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 4 | reader 数（≤16）；writer 固定 1 个，不在该数内 |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | magic/随机 key 种子，失败输出带出 |
| `STRESS_FIXED_ROUNDS` | 16 | `seconds=0` 时 writer 轮数；grow/delete/reinsert 约按 6:3:1 分配 |
| `STRESS_MAP_MAX` | 1<<20 | 增长上限（key 数，范围 1024..1<<22）；自检用 `1<<16` 加速 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

# 失败判读

- `canary mismatch ...`：节点被提前回收/复用或搬迁写坏（重点看 phase 与 key）；
- `present=... want=...`：map 存在性与 phase/frontier 模型不一致（插入/删除边界错误或搬迁丢 entry）；
- `iterated unexpected key`：遍历看到了本阶段不应存在的 key（旧 bucket 残留/stale entry）；
- `expected key present before delete`：删除阶段 key 丢失（搬迁/删除路径错误）；
- `map read-back pointer mismatch`：插入后立即读回不一致（map 写入损坏）；
- `node gen=... want=...`：重插节点代数错误（旧节点未清空/present 视图错误）；
- `map len=... want=...`：扩容/删除/重插后的元素计数错误；
- `no progress`：watchdog 超时（writer 卡死/全局锁问题），附全栈；
- `goroutine leak`：reader/writer 未退出；
- 只在 clobberfree/soak 下失败：UAF/提前回收；base 也失败多为 phase 模型/锁语义错误。

# 运行

```bash
# 长跑（百万级 entry）
SUITE=stress FILTER=u12 STRESS_SECONDS=60 PROFILES=soak,soak-single ./run.sh
# 快速自检（65536 entry）
SUITE=stress FILTER=u12 STRESS_SECONDS=3 STRESS_MAP_MAX=65536 PROFILES=soak,soak-single ./run.sh
SUITE=stress FILTER=u12 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 STRESS_MAP_MAX=65536 PROFILES=base,clobberfree ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=u12 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=3 STRESS_MAP_MAX=65536 PROFILES=base ./run.sh
```
