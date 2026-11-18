---
id: l09_cgo_bitfield_header
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，紧凑位布局是 C 数据面常见形态）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，位域协议头边界）
tags: [L09, cgo, bitfield, signed-bitfield, masking, layout, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库中的位域协议头：`hdr_t` 含 `4/4/8/16 + 16/3/13 + 6/20/6` 位字段，其中 `delta` 是 6 位有符号字段。cgo 不能直接访问 bit-field，全部经 C 辅助函数读写；验证最大/最小位宽值、无符号位域按 2^width 取模、字段独立性、结构体与字节数组往返、短缓冲拒绝，以及跨多轮 GC 的稳定性。C 只在调用期内访问 Go 上的结构体/字节切片，不保留 Go 指针。

# 期望行为

1. Go `unsafe.Sizeof(hdr_t)` 与 C `sizeof(hdr_t)` 一致；
2. 全字段最大/最小值（含 `delta=-32`、`delta=31`）可正确写入/读出；
3. 无符号位域超宽赋值按掩码取模（`tos=300→44`、`win=2000000→951424`、`pad=100→36`），且邻字段不变；
4. 单字段修改不影响其他字段；
5. 32 组随机化字段值经 C `memcpy` 转字节、清零、再转回后完全一致；
6. 字节缓冲不足 `sizeof(hdr_t)` 时返回 -1 且不改写结构体；
7. 8 轮显式 GC 后字段值稳定；
8. 最后一行 `ALL PASS l09_cgo_bitfield_header`。

# 失败判读

- `sizeof hdr_t`：cgo 对 bit-field 结构的翻译布局与 C 不一致；
- `max/zero roundtrip`：位宽截断、符号扩展错误；
- `signed delta`：6 位有符号字段未按二补码解释；
- `masks to`：无符号位域赋值未按 2^width 取模，或字段被相邻位污染；
- `byte roundtrip`：位域打包不可逆/跨存储单元布局理解错误；
- `short buffer`：C 辅助函数未检查长度，越界读；
- GC 后失败：C 保留了 Go 指针（本用例只同步读写 Go 结构体与字节切片）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_bitfield_header
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
