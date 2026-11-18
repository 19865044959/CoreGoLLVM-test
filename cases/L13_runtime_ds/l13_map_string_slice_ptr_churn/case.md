---
id: l13_map_string_slice_ptr_churn
dim: L13_runtime_ds
priority: P2
sources:
  - badger@v4.5.0: stream_test.go:174 TestStreamMaxSize 流式 KV 前缀计数 map 的大规模写入/统计
  - etcd@v3.5.0: server/mvcc/watcher_test.go:33 TestWatcherWatchID watcher 表 map[*sub] 的指针值查找
  - sonic@v1.13.2: issue_test/issue100_test.go:30 TestLargeMapValue >128B value 以指针存入 map 桶
tags: [L13, map, slice-of-pointers, churn, truncate, snapshot, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 4 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟“倒排索引/标签到记录列表”的二级索引：`map[string][]*record`，value 是含 id、tag、name、40B pad 的指针切片。初始 40 个 tag 各挂 0..5 条记录，随后每轮 240 次确定性随机操作：批量追加、截断、删除 tag、整组重建、超容量批量追加；每轮后显式 GC + 垃圾分配，并按排序后的 key 与独立 `map[string][]int` 模型逐项比对（长度、id 顺序、name、pad 字节）。过程中最多冻结 48 个 tag 并保存其旧切片头，之后只允许整组删除；最终 GC 后这些快照仍必须指向原来的记录（专门验证旧底层数组仅由 snapshot 保活）。

# 期望行为

1. 初始构建后 map 与模型的外层 key 集合、每个 tag 的记录长度/id/内容完全一致；
2. 每轮 churn 后：删除的 tag 不在、重建的 tag 内容正确、截断保留前缀、追加记录 id 单调，所有叶子指针非 nil 且 name/pad 未损坏；
3. 每轮都执行显式 GC + 垃圾分配，GC 后上述比对仍成立；
4. 被冻结 tag 若后续从 map 删除，其保留下来的 48 个以内快照仍能读出原来的记录序列（id、tag、name 一致）；
5. nil map 读取长度为 0、对 nil map 执行 delete 不 panic，空 map 删除不存在的键保持为空；
6. 最后一行 `ALL PASS l13_map_string_slice_ptr_churn`。

# 失败判读

- 按排序 key 比对时长度或 id 顺序不一致：截断/追加的切片头维护错误；
- GC 后出现 nil 记录或 name/pad 乱码：map 桶或切片中的指针未被扫描（写屏障/位图问题）；
- 仅 efence/clobberfree 下快照内容变化：旧底层数组被提前回收（UAF）；
- 快照内容被新写入覆盖：冻结逻辑失效，append 复用了截断后的底层数组。

# 参数

- `UAF_SEED`：操作序列、tag 选择与记录内容的种子，失败输出带该种子；
- `TORTURE_ROUNDS`：churn 轮数，默认 4，范围 1..500。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_string_slice_ptr_churn/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=50 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
