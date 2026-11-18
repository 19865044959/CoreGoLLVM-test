---
id: l17_chan_pointer_handoff
dim: L17_stackmap
priority: P1
sources:
  - badger@v4.5.0: db_test.go:1246 TestGetSetRace（无缓冲 chan 交接写入的 key/数据）
tags: [L17, stackmap, chan, unbuffered, noinline, GC]
requires: []
runtime: "<5s"
---

# 场景

模拟 badger `TestGetSetRace` 的写入协程→读取协程路径：写协程分配一条含指针、指针切片、字符串切片和 `[]byte` 的记录，经**无缓冲 chan** 把 `*record` 交接给读协程；交接完成后发送方继续分配垃圾并 `runtime.GC()`，接收方持有同一指针再 GC。双方在 GC 后分别逐字段校验，证明 chan 交接点、发送/接收栈帧中的指针位图正确。

# 期望行为

1. 每轮交接的 `record` 在双方 GC 后 `id/label/head/next/list/arr/strs/blob` 全部与构造值一致；
2. 无缓冲 chan 每轮完成一次同步交接，24 轮无死锁；
3. 发送方在接收方校验后再次 GC、再次校验仍一致；
4. 退出码 0，最后一行 `ALL PASS l17_chan_pointer_handoff`。

# 失败判读

- `FAIL: sender*: record corrupted after GC` / `receiver*`：交接指针在某一侧栈槽未被标记，或 chan send/recv 路径漏标；
- 字段值变成垃圾字节（clobberfree 下常见 `0xde...`）：对象已被提前回收；
- SIGSEGV / `fatal error: found bad pointer`：漏标导致堆对象被释放；
- 挂起超时：无缓冲 chan 交接两侧未配对。

# 参数

无随机路径；`UAF_SEED` 不影响结果。循环 24 轮固定，每轮两侧共 3 次 `gcStress`。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_chan_pointer_handoff/*.go
GODEBUG=clobberfree=1 /tmp/case
```
