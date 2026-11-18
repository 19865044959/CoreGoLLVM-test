---
id: l07_unaligned_scalar_load
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - webrtc@v4.1.2: pkg/media/rtpdump/rtpdump_test.go:70 固定宽度二进制 Header/Packet 往返编解码
  - badger@v4.5.0: y/y_test.go:44 512B 页缓冲跨页写 256B/1KB，覆盖非对齐偏移
  - pgx@v5.7.4: pgproto3/chunkreader_test.go:9 已缓冲数据按偏移读取，耗尽后重置
tags: [L07, unsafe, unaligned, scalar-load, little-endian, interior-pointer]
requires: []
runtime: "<3s"
---

# 场景

非对齐标量读写：在 `[96]byte` 堆缓冲的 0/1/2/3/5/7/9/13/17/23 等偏移上用
`*(*uint16)(unsafe.Add(base, off))`、`*uint32`、`*uint64` 读取，与
`encoding/binary.LittleEndian` 及逐字节移位拼装三方对照；再在奇数偏移写入后逐字节校验并恢复。
另在结构体字节视图上做非对齐 uint32 读取。最后只保留奇偏移的内部 uint32/uint64 指针，
丢弃缓冲变量，跨显式 GC 读取并用字节视图校验邻居。

## 期望行为

1. 所有 uint16/uint32/uint64 非对齐读取等于 binary.LittleEndian 结果与手工拼装值；
2. 测试偏移中至少各有一个对 4 字节/8 字节非对齐（记录计数 >0）；
3. 非对齐写入按小端落字节，类型化回读一致；恢复后输入与原始快照完全相同；
4. 结构体字节视图上的非对齐 uint32 读取与逐字节拼装一致；
5. 丢弃缓冲、仅保留内部 uint32/uint64 指针并 GC 后，值与 8 字节视图仍正确；
6. 内部指针偏移差等于预期（uintptr 仅用于计算/比较，不跨安全点保存）；
7. 末行 `ALL PASS l07_unaligned_scalar_load`，失败退出 1。

## 失败判读

- 非对齐值与 binary/拼装不符：非对齐 load 的 codegen、端序或偏移错误；
- 写入字节序不符：非对齐 store 生成错误；
- GC 后崩溃/efence 报错：内部指针未被视为整对象根（应按 4/8 字节对齐的假定被打破）；
- SIGBUS/SIGSEGV：目标平台不支持非对齐访问（先确认平台，本用例面向 amd64/arm64）；
- 缓冲被修改：恢复步骤遗漏。

## 参数

- `UAF_SEED`：决定缓冲字节模式与最终保留指针的偏移（5..7），失败信息带种子；
- 无 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_unaligned_scalar_load/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
