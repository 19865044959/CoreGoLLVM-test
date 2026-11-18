---
id: l18_map_old_key_new_value_grow
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - badger@v4.5.0: db_test.go:249（20 goroutine 共写 1 万 key，扩容后全量顺序校验）
tags: [L18, write-barrier, map-growth, old-key, young-value, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 6 轮；TORTURE_ROUNDS 放大轮数，上限 32）"
---

# 场景

老 map 预置 128 个老 key（key 字符串与初始 value 均经历两轮 GC）。每轮：

1. 按 `UAF_SEED` 随机顺序用 noinline `rewriteExisting` 覆写全部老 key，值为全新年轻对象；
2. 用 noinline `insertNew` 插入 128 个全新年轻 key/value，推动 map 从 128 槽持续扩容；
3. 每 32 次覆写穿插一次 GC，批量结束再 GC，按老 key 逐一校验 value 内容与代次，并校验 map 长度。

`insertNew` 内分配 key 字符串和 value，返回后两者只被老 map 引用。

# 期望行为

1. 每轮 GC 后，128 个老 key 仍能命中，value 的 id/ver/tag/48 字节模式正确；
2. 所有已插入的新 key 在 GC 后仍可命中且内容正确；
3. `len(map)` 每轮恰为 `128 + 128*round`；
4. 失败输出带 `seed=<UAF_SEED>`，同种子可复现；
5. 退出码 0，最后一行 `ALL PASS l18_map_old_key_new_value_grow`。

# 失败判读

- `map entry id=... corrupted`：老 map 写入年轻 key/value 时漏写屏障，或扩容搬迁丢失新值；
- `map size drift after growth`：扩容/插入路径丢 entry（不是 GC 问题，先查 key 生成）；
- clobberfree 下 value 字段变垃圾、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现随机覆写顺序。

# 参数

- `UAF_SEED`：决定每轮覆写老 key 的随机顺序（默认 1；runner 每次注入不同值），不影响输出文本；
- `TORTURE_ROUNDS`：轮数倍数（默认 6，上限 32）；每轮固定新增 128 个 key/value，轮数越大扩容次数越多。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_map_old_key_new_value_grow/main.go
UAF_SEED=7 TORTURE_ROUNDS=16 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
