---
id: l13_hashmap_collision_probe
dim: L13_runtime_ds
priority: P2
sources:
  - go-redis@v9.8.0: probabilistic_test.go:60 布隆过滤器哈希碰撞导致基数统计不精确的边界
  - tailscale@v1.80.2: util/deephash/deephash_test.go:896 10 万次哈希必须无碰撞的穷举校验
  - quic-go@v0.50.1: packet_handler_map_test.go:45 连接 ID 冲突时拒绝加入 handler map
tags: [L13, map, hash-collision, separate-chaining, tombstone, rehash, pointer-value, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 2 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

用 8 桶链式表故意制造 map 哈希冲突，模拟冲突链上的查找/覆盖/删除与重哈希：

1. `UAF_SEED` 派生 key 前缀，每轮插入 2400 个 key；8 个桶必然形成长链（断言 max 桶至少 n/8 且 >1）；
2. 按随机顺序用新建字符串 key 逐项查找，value 指针与内容必须精确；
3. 每 6 个 key 覆盖 value，旧指针返回且链长度不变，GC 后复查；
4. 每 3 个 key 删除：链内前移压缩、尾槽清零，删除项立即不可见，GC 后验证；
5. 重插删除项，再将 8 桶重哈希到 64 桶，逐项验证后 GC；
6. 聚合 `sum(id%1000)` 与模型一致；旧表仍被 KeepAlive，验证新旧表指针不串。

每个阶段边界执行显式 `runtime.GC()` + 垃圾分配；`TORTURE_ROUNDS` 控制轮数（默认 2）。

# 期望行为

1. 冲突链存在且桶内总数为 n，随机查找全部命中正确指针；
2. 覆盖返回旧值、长度不变；删除后不可见且不误删同桶邻居；
3. 重哈希后 64 桶布局仍能命中全部条目，GC 前后一致；
4. 冲突链、墓碑压缩与 value 指针跨 GC 不被破坏；
5. 最后一行 `ALL PASS l13_hashmap_collision_probe`。

# 失败判读

- `bucket not congested`：哈希/桶索引计算错误，未覆盖冲突路径；
- `lookup mismatch`：链内 key 比较或删除压缩丢条目；
- `deleted key resurrected`：删除只清 value 未删条目；
- `payload corrupt` / GC 后错乱：冲突链元素指针未被扫描（UAF）；
- `rehash count`：重哈希遗漏桶/条目。

# 参数

- `UAF_SEED`：key 前缀、value 内容与探测顺序种子，失败输出带该值；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1..200。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_hashmap_collision_probe/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=50 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
