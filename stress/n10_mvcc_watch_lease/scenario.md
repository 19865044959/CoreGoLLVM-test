---
id: n10_mvcc_watch_lease
priority: P2
covers: [L02, L05, L13, L18]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

纯内存 revisioned KV + watcher + lease 长跑。随机 op 生成器由 `UAF_SEED` 派生，多 worker 并发执行 put / delete / get / range / lease grant·renew·revoke / watch·cancel / compact：

- 每次键变更递增全局 `rev` 并写入历史事件流；watcher 支持 prefix、range 与 start-rev（回放历史、可被 compaction 钳制），事件在 store 锁内按 rev 顺序入队，每个 watcher 独立 consumer goroutine 排空；
- lease 具备 TTL 与虚拟时钟：`advance(delta)` 推进时钟并在同一临界区到期删除附着 key（不以真实 sleep 驱动）；revoke/expire 产生 delete 事件；
- compaction 只裁剪 `min(活跃 watcher nextRev)-1` 以下的历史，保证活跃 watcher 重放所需的旧事件仍在；
- 每个 watcher 的期望事件集在注册与 emit 时同步累积；cancel 时 consumer 排空后与期望集逐条比较（不丢/不重/严格 rev 递增）。

数据侧持续校验：写后读 revision（丢失写/墓碑不可见立即 FAIL）、range 有序性、value 的 lease 附着关系与“已过期 lease 不得仍附着”；独立 verifier 全量检查 map/lease/watch 元数据。结束做一次虚拟时间大推进（drain 所有 lease）并验证零活跃 lease / 零活跃 watcher / goroutine 基线。

## 不变量

1. 每个 watcher 收到的事件序列必须与注册/emit 时累积的期望序列完全一致：数量、顺序、rev、key、value、del、lease 字段逐条相等。
2. 投递事件 rev 严格递增，非匹配事件不得投递；start-rev 被钳制到 `[compactRev+1, rev+1]` 后记录实际值参与模型比较。
3. compaction 不得丢弃活跃 watcher 在 `nextRev` 之前可能需要的历史；`compactRev ≤ rev` 始终成立。
4. lease 到期仅由虚拟时钟触发；到期/revoke 后附着 key 被删除并产生墓碑事件；任何 value 的 `lease` 字段要么为 0，要么指向存在且未过期的 lease 且双向附着一致。
5. 写后立即读：读到的最新 revision 不得小于写入 revision；若相等则内容必须一致（put）或不可见（delete）。
6. 结束 drain 后 `active leases == 0`、`active watchers == 0`，goroutine 回到基线 +2 内。
7. 心跳随每个 op 增长，watchdog 在 6×H 无进展时 FAIL 并打印全部 goroutine 栈。
8. 结束打印 metrics 与 `STRESS PASS n10_mvcc_watch_lease`。

## 参数

- `STRESS_SECONDS`（默认 60，`0`=固定轮数模式）
- `STRESS_WORKERS`（默认 4，上限 32）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`（默认 1，决定 op 序列、watch 过滤条件与 lease TTL）
- `STRESS_FIXED_ROUNDS`（默认 24，仅 `STRESS_SECONDS=0` 时生效；每 worker 的轮数，每轮 16 个 op）

键空间固定为 8 × 32 个 `a/00`…`h/31`；每 worker 最多 6 个活跃 watcher、4 个活跃 lease。

## 失败判读

- `watcher ... expected_events=N actual=M`：watch 事件丢失或重复；
- `watcher replay mismatch (lost/reordered)`：事件内容/顺序与模型不一致（典型为 compaction 提前裁剪或并发投递乱序）；
- `delivered non-matching key` / `event rev not increasing`：watch 过滤器或入队顺序错误；
- `dangling lease` / `expired lease still attached` / `lease attachment missing`：lease 到期删除或附着关系错误；
- `lost write` / `read-after-put value mismatch` / `tombstone invisible`：数据路径 revision 语义破坏；
- `watchdog stalled`：store 锁/consumer 死锁或调度饥饿；
- `goroutine leak`：watcher consumer 未退出；
- 仅在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress FILTER=n10 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=n10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n10 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
SUITE=stress FILTER=n10 STRESS_SECONDS=3 PROFILES=soak,soak-single REPEAT=1 ./run.sh
```

## 来源

- `repos/etcd/server/mvcc/watchable_store_test.go:225-299,532-654`（revisioned KV / watch / compaction）
- `repos/etcd/server/lease/lessor_test.go:286-337,407-459`（lease grant/renew/revoke/expire）
- <https://github.com/etcd-io/etcd/blob/main/tests/robustness/README.md>（随机负载 + watch/lease/compaction）
- <https://github.com/cockroachdb/cockroach/blob/master/pkg/kv/kvnemesis/doc.go>（随机 op + 模型检查）
