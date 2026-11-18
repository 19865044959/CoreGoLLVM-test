---
id: l18_pool_finalizer_chan_handoff
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: y/y_test.go:301（TestAllocatorReuse：sync.Pool 式分配器 Reset 后复用）
  - nats-server@v2.11.0: server/norace_test.go:10072（TestNoRaceConnectionObjectReleased：池/连接对象 finalizer 释放）
  - badger@v4.5.0: db_test.go:1246（TestGetSetRace：chan 同步交接后读侧校验值）
tags: [L18, sync.Pool, finalizer, channel, reachability, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

全局 `sync.Pool` 的 `New` 创建带 finalizer 的对象（finalizer 只写原子标记）。老 hub 持有已晋升的缓冲 chan（cap=256）：

1. 从池取 128 个对象写入老 chan，跨两轮 GC；断言 len(chan)==128 且所有对象未被终结；
2. 取出一半到全局 held slice，再 GC；断言“半 held 半在 chan”期间无对象被终结，held payload 完好；
3. 取出剩余一半，GC；断言全部 held 期间无对象被终结、payload 完好；
4. 把 64 个对象放回池但保留外部引用，GC；池清空不得终结这些对象；
5. 丢弃池引用、chan 与 held，GC 轮询至 128 个对象全部被终结。

# 期望行为

1. 对象被老 chan 缓冲、全局 held slice 或外部引用持有时 finalizer 标记恒为 0；
2. 每次 GC 后 held 对象的 ver 与 24 字节 data 模式完整；
3. 放回池且仍有外部引用的对象不被终结；
4. 释放全部根后所有对象在 5s 内最终被终结；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_pool_finalizer_chan_handoff`。

# 失败判读

- `finalizer ran while objects in chan`：channel 缓冲未被视为根，或 hchan 指针被误回收；
- `finalizer ran while ... held`：可达对象被误判死亡（池/外部引用存活判定错误）；
- `held object ... data corrupted`：池复用覆盖了仍被外部持有的对象，或对象被提前释放；
- `not finalized after release`：对象被隐藏引用保活（先查用例残留局部变量）；
- 只断言“可达不终结 / 失活最终终结”，不依赖终结顺序。

# 参数

- `UAF_SEED`：payload salt 与失败信息；
- 无 TORTURE 参数；等待上限固定 5s。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_pool_finalizer_chan_handoff/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
