---
id: l07_layout_prefix_cross_type
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 零拷贝重解释为 string
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 unsafe 拆解结构体/接口头与字段偏移
  - yaegi@v0.16.1: internal/unsafe2/unsafe_test.go:10 reflect.StructOf 造结构体并改写字段类型
tags: [L07, unsafe, struct-layout, prefix-compat, cross-type-cast, write-barrier, GC]
requires: []
runtime: "<10s"
---

# 场景

跨类型的前缀布局：`lfExtA` 与 `lfExtB` 不嵌入公共类型，但前 40 字节的字段类型与顺序完全
一致（`int64/string/*leaf/int32`），尾部各自不同（A：`[]byte/*leaf/uint64`，B：
`[4]uint64/*leaf`）。断言两种类型的 `Offsetof` 逐字段相等、后缀起点等于 `Sizeof(prefix)`、
总大小相同；随后用 `(*lfPrefix)(unsafe.Pointer(ext))` 读写公共前缀，并通过字段偏移直接
改写另一类型的前缀字段。混合 `[]*prefix` 在 GC 后仍需按各自真实类型扫描后缀指针。

## 期望行为

1. 两个扩展类型的 id/name/link/code 偏移完全一致，且与 `lfPrefix` 相同；
2. A 的 `data`、B 的 `table` 起点都等于 `unsafe.Sizeof(lfPrefix{})`，两者总大小相等；
3. 通过 `*lfPrefix` 读取的 id/name/link/code 与完整类型字段一致；
4. 通过前缀视图写 name/link 后完整类型立即可见；通过 `unsafe.Add` 字段偏移写 B 的
   name/link 同样可见；
5. `[]*prefix` 按后缀偏移回读两种真实类型，尾部 data/table/tail 全部正确；
6. 丢弃完整类型变量后，仅 `[]*prefix` 跨显式 GC，A/B 的后缀指针仍被扫描且内容完整；
7. GC 后继续通过 `*prefix` 写前缀不破坏各自后缀；只保留尾部叶指针再 GC 仍有效；
8. 末行 `ALL PASS l07_layout_prefix_cross_type`，失败退出 1。

## 失败判读

- Offsetof 不等：字段类型/顺序不一致（用例自身问题），或编译器布局计算错误；
- 前缀写入不可见：`unsafe.Pointer` 转换落到错误地址或字段偏移错误；
- GC 后后缀指针为空/崩溃：GC 只按 `*prefix` 的静态类型扫描，未按堆对象真实类型扫描尾部；
- 后缀内容错乱：前缀写入越界覆盖尾部（尺寸断言应拦住）；
- 仅写屏障相关模式失败：通过偏移写入指针字段未触发写屏障。

## 参数

- `UAF_SEED`：决定前缀与尾部字段的值模式，失败信息带种子。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_layout_prefix_cross_type/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
