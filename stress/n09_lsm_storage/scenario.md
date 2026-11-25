---
id: n09_lsm_storage
priority: P2
covers: [L02, L05, L13]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

纯内存 mini-LSM 长跑：

- WAL：内存记录（seq/key/val/del/checksum），写路径抽样校验 checksum 与 seq 单调，flush 后按已落盘 maxSeq 截断；
- memtable：`map[string][]version` + skiplist 有序索引，多版本按 seq 升序；
- 不可变 memtable 队列 + 专职 flusher 与 L0/L1 SSTable（内存有序数组，`key asc, seq desc`）；
- compaction：合并 L1 + 全部当前 L0，按 `min snapshot seq` 淘汰旧版本；第一个 ≤ safe 的版本若为墓碑则连同更旧版本一起丢弃（墓碑清理），合并期间新 flush 的 L0 不受影响；
- 快照迭代器：快照注册与写临界区同步，读取 `seq ≤ snapshot` 的最新版本；分锁方案为 writeMu（WAL/memtable/model）、memtable RWMutex、db 表链 RWMutex、flushMu、compactMu。

随机 op（seed 派生）并发执行写/删/读/快照范围扫描/compaction。每个 worker 维护快照 digest：同一快照在后续写入与 compaction 之后重扫，digest 必须完全一致（快照隔离）。写后读校验 revision 语义；value 带 crc32，读取/扫描时校验。收尾 flush 全部 memtable、force compaction、校验 WAL 截断与 `imm=0 l0=0 snaps=0`，并做全量扫描 + 逐 key Get 与独立 model 对比。

## 不变量

1. 写后读：put 的最新 revision 不得小于写入 revision，相等时内容必须一致；delete 后只禁止**旧值复活**（读到比墓碑更旧的活值）；墓碑连同旧版本被 compaction 合法回收（Get 返回 key 不存在）允许。
2. 所有 memtable/SSTable 的读与快照扫描必须返回严格升序且去重的 key；value crc32 必须与写入时一致。
3. 同一 snapshot seq 与 range 的扫描结果（digest + 条目数）在并发写入、flush、compaction 之后保持完全一致。
4. compaction 对每个 key 保留：`seq > minSnapshot` 的全部版本 + 第一个 `seq ≤ minSnapshot` 的版本；后者为墓碑时可整体丢弃。L0/L1 切换与 flush 安装对读者必须是原子的（不能出现 memtable 已出队而 SSTable 未安装的窗口）。
5. WAL 记录 seq 严格递增、checksum 正确；flush 后只保留未落盘记录，最终必须为空。
6. 结束时所有快照已释放、immutable 队列与 L0 为空、L1 为最终整表；全量扫描与逐 key Get 必须与 model（writeMu 内同步更新）逐 key 完全一致。
7. 心跳随 op 增长；watchdog 6×H 无进展 FAIL；goroutine 回到基线 +2 内。
8. 结束打印 metrics 与 `STRESS PASS n09_lsm_storage`。

## 参数

- `STRESS_SECONDS`（默认 60，`0`=固定轮数模式）
- `STRESS_WORKERS`（默认 4，上限 32）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`（默认 1，决定 op/key/value 序列与 skiplist 随机层高）
- `STRESS_FIXED_ROUNDS`（默认 24，仅 `STRESS_SECONDS=0` 时生效；每 worker 的轮数，每轮 16 个 op）

键空间 256；memtable 在 256 个版本或 128KB 时轮转；immutable 队列超过 3 时写者协助 flush；L0 ≥ 2 时触发 compaction。

## 失败判读

- `lost write` / `lost tombstone / resurrection` / `read-after-put mismatch`：读写路径 revision 语义错误或墓碑被旧版本"复活"；
  （墓碑被 compaction 合法回收、Get 返回 key 不存在**不算失败**——无快照钉住时墓碑+旧版本整体丢弃是 LSM 的正常语义，2026-11-20 内网压测报告已确认旧断言为误报。）
- `value checksum mismatch` / `snapshot value checksum mismatch`：value 缓冲区被破坏或错误版本可见；
- `snapshot isolation broken` / `digest changed`：旧版本被过早淘汰、flush/compaction 存在读者可见的真空窗口，或快照边界与写临界区不同步；
- `snapshot scan not strictly ordered`：合并/迭代器排序错误；
- `WAL ... checksum` / `seq increasing` / `WAL truncated`：WAL 记录生命周期错误；
- `phase=final ... expected=deleted actual=present`：墓碑被过早清理或最终 compaction 错误；
- `watchdog stalled`：锁顺序死锁或 flush/compaction 饥饿；
- `goroutine leak`：flusher/compactor 未退出；
- 仅在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress FILTER=n09 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=n09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n09 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
SUITE=stress FILTER=n09 STRESS_SECONDS=3 PROFILES=soak,soak-single REPEAT=1 ./run.sh
```

## 来源

- `repos/badger/levels_test.go:757-877`（level compaction / 版本淘汰）
- `repos/badger/db_test.go:447-520`、`repos/badger/db2_test.go:577-676`（并发读写/迭代/重开）
- `repos/badger/manifest_test.go:254-290`（WAL/manifest 生命周期）
