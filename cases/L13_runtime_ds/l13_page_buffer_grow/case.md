---
id: l13_page_buffer_grow
dim: L13_runtime_ds
priority: P0
sources:
  - badger@v4.5.0: y/y_test.go:44,70 多页缓冲跨页写、容量翻倍，并与 bytes.Buffer 逐轮对照
tags: [L13, page-buffer, slice-growth, bytes.Buffer, truncate, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 badger y 包的页式缓冲：以固定 512B 页追加写入，跨越页边界；Truncate 到非页边界后重写；再从 32B 起逐轮翻倍写入，与 `bytes.Buffer` 的输出逐轮对照。GC 插在跨页写完成后、Truncate 后和翻倍循环中，验证页切片（`[][]byte`）在堆上的存活与内容完整。

# 期望行为

1. 依次写入 256B/1024B/511B/513B 后总长 2304B，页数为 5，内容与 `bytes.Buffer` 完全一致；
2. 每个页长度固定为 512B；
3. Truncate 到 700B 后重写 900B，页缓冲与 `bytes.Buffer` 内容一致，页数按 `ceil(n/512)` 重算；
4. Truncate 到 0 后写入 600B，重建 2 个页且内容正确；
5. 32B 起翻倍 10 轮（总长 32736B）后，页缓冲与 `bytes.Buffer` 内容一致，`bytes.Buffer.Cap()` 不小于总长；
6. 最后再 GC 一次，两个页缓冲的内容仍然有效；
7. 最后一行 `ALL PASS l13_page_buffer_grow`。

# 失败判读

- 跨页内容错位/旧数据残留：跨页写入的偏移计算或 Truncate 清零有误；
- 仅 clobberfree/efence 下内容变成垃圾：页切片未被正确扫描，底层数组被回收；
- 页数与 `ceil(n/512)` 不符：Truncate/Write 后页表长度维护错误；
- 翻倍轮次内容不一致：append/扩容路径写越界或旧数据未拷贝。

# 参数

无随机路径，不使用 `UAF_SEED`。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_page_buffer_grow/*.go
GODEBUG=clobberfree=1 /tmp/case
```
