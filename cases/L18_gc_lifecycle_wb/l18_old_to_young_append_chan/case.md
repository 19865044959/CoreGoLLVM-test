---
id: l18_old_to_young_append_chan
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: C16 写屏障（append 扩容 / chan 缓冲）
tags: [L18, write-barrier, append, chan, old-gen]
requires: []
runtime: "<3s"
---

# 场景

老对象的 slice 通过 append 扩容后写入新对象（新底层数组、旧 slice header 被更新），以及老 chan 缓冲区接收年轻对象。两处都依赖写屏障保住年轻对象。

# 期望行为

1. 每轮 append 后 GC，最后一个元素与所有旧元素都满足 `v%16==7,name=="y"`；
2. 每轮 chan 发送后 GC，接收到的对象仍有效；
3. slice 长度按预期增长到 24；
4. 最后一行 `ALL PASS l18_old_to_young_append_chan`。

# 失败判读

- `old slice append lost young object`：append 扩容时旧对象里的 slice header 更新未走写屏障；
- `old chan buffer lost young object`：channel 缓冲写入未走写屏障/未被扫描；
- 仅在 clobberfree/efence 下失败：年轻对象已被回收。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_to_young_append_chan/*.go
GODEBUG=clobberfree=1 /tmp/case
```
