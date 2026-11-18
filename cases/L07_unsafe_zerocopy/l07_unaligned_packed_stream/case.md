---
id: l07_unaligned_packed_stream
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - webrtc@v4.1.2: pkg/media/rtpdump/rtpdump_test.go:70 固定宽度二进制 Header/Packet 往返编解码
  - badger@v4.5.0: y/y_test.go:44 512B 页缓冲跨页写 256B/1KB，覆盖非对齐偏移
  - pgx@v5.7.4: pgproto3/chunkreader_test.go:9 已缓冲数据按偏移读取，耗尽后重置
tags: [L07, unsafe, unaligned, packed-struct, little-endian, interior-pointer, GC]
requires: []
runtime: "<3s"
---

# 场景

非对齐的紧凑记录流：记录步长 13 字节，布局为 `tag(1) + u32(4) + u64(8)`，因此 u64 字段
相对记录起点偏移 5，随记录步进覆盖全部 8 个地址对齐类。用类型化指针
`*(*uint64)(unsafe.Pointer(&buf[base+5]))` 读取/写入，与 `encoding/binary.LittleEndian`
和逐字节视图对照；再从起点 0..4 做偏移阶梯遍历，确认跨记录的非对齐访问一致。模拟 RTP
固定宽度头、页缓冲跨页写与协议 chunkreader 的按偏移读取。

## 期望行为

1. 每条记录的 u32/u64 类型化读取等于 `encoding/binary` 解码值；
2. u64 地址覆盖 0..7 全部对齐类，且同时存在对齐与非对齐地址；
3. 起点偏移 0..4 的阶梯遍历中所有 u64/u32 读取与字节层小端解码一致；
4. 非对齐写入后字节层内容正确，tag 字段不被破坏；恢复后整块缓冲与初始副本一致；
5. 末记录的 u64 恰在有效区末尾，读取不越界；
6. 仅保留内部 u32/u64 指针并显式 GC 后仍可读，且 u32 指针与 u64 指针相差 4 字节；
7. 末行 `ALL PASS l07_unaligned_packed_stream`，失败退出 1。

## 失败判读

- 类型化读取与 binary 不一致：非对齐访问生成错误宽度/字节序的加载；
- 对齐类缺失：记录步长或字段偏移计算错误（用例自身问题）；
- 写入破坏 tag：u32/u64 存储宽度或偏移错误；
- GC 后崩溃/值错：内部指针未被当作对象根，或 slice 数据被提前回收；
- 仅特定 GOARCH 失败：非对齐访问在该架构上需要多指令序列，编译器生成路径有问题。

## 参数

- `UAF_SEED`：决定记录数 12..16 与字段值模式，失败信息带种子。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_unaligned_packed_stream/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
