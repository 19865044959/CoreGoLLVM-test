---
id: l07_alignment_probe_dispatch
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - goccy-json@v0.10.5: size_test.go:10 64 位下结构体大小与字段布局断言
  - fasthttp@v1.59.0: round2_64_test.go:10 按对齐/2 的幂向上取整
  - webrtc@v4.1.2: pkg/media/rtpdump/rtpdump_test.go:70 固定宽度二进制 Header/Packet 往返编解码
tags: [L07, unsafe, alignment-probe, copy-dispatch, typed-load, guard-byte, GC]
requires: []
runtime: "<3s"
---

# 场景

运行时对齐探测与分派：`copyDispatch` 读取 src/dst 数据指针的低 3 位（单表达式
`uintptr(p)&7`），两者都 8 字节对齐走 `uint64` 批量拷贝，4 字节对齐走 `uint32`，
2 字节对齐走 `uint16`，否则逐字节 `copy`；尾部不足一个字的字节逐字节补齐。测试覆盖
同对齐类 0..7、64 种 src×dst 对齐组合、12 种长度边界与短目标拒绝，并在前后放置保护
字节验证不越界。模拟二进制编解码与 2 的幂对齐分配器的快速路径选择。

## 期望行为

1. 同对齐类 0..7 分别使用 word/u16/u32/byte 路径，四个路径全部被覆盖；
2. 64 种 src/dst 对齐组合的分派类型完全由低 3 位决定（`wantKind` 表）；
3. 所有组合与长度边界下，目标内容与源逐字节一致；
4. 目标区域之后的保护字节保持 0xaa，短目标（len(dst)<len(src)）被拒绝且零写入；
5. 偏移 0..7 恰好覆盖全部 8 个地址对齐类；
6. 对齐的字指针跨显式 GC 后仍可读，字前后保护字节不被破坏；
7. 末行 `ALL PASS l07_alignment_probe_dispatch`，失败退出 1。

## 失败判读

- 分派类型与 wantKind 不符：对齐探测或分支条件错误；
- 内容不一致：批量路径宽度/尾部补齐错误，或未对齐时使用了错误宽度的类型化访问；
- 保护字节被改：批量循环多写一个字（边界条件 off-by-one）；
- 短目标仍写入：长度检查缺失；
- GC 后崩溃：内部字指针未被当作根；
- 仅特定 GOARCH 失败：非对齐类型化访问需要多指令序列，编译器生成路径有问题。

## 参数

- `UAF_SEED`：决定拷贝长度 19..31、源字节模式与保护字值，失败信息带种子。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_alignment_probe_dispatch/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
