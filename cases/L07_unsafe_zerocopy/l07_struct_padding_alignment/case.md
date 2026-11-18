---
id: l07_struct_padding_alignment
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - goccy-json@v0.10.5: size_test.go:10 TestOpcodeSize 断言 64 位下结构体大小与字段布局
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 TestDumpOpcode unsafe 拆解结构体/接口头
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 重解释为 string
tags: [L07, unsafe, struct-layout, padding, alignment, Offsetof]
requires: []
runtime: "<3s"
---

# 场景

序列化/池化对象的布局测试：结构体含 `uint8/uint64/uint16/*pnode/uint32`，存在内部与尾部 padding。用例断言 64 位下的 Offsetof/Sizeof/Alignof，用 `unsafe.Slice` 把结构体解释为字节视图，验证字段写入不触碰 padding、padding 写入不影响字段、嵌套结构体偏移、数组元素步长，以及仅由字节视图或元素指针保活时跨 GC 的正确性。

# 期望行为

1. `padded` 的 size=40、align=8，字段偏移依次为 a=0、b=8、c=16、d=24、e=32；
2. 字段地址按 8 字节对齐（单表达式取模，不保存 uintptr）；
3. 零值结构体的 padding 区间 [1,8)、[18,24)、[36,40) 全为 0；
4. 普通字段写入、`unsafe.Add`+Offsetof 写入、指针槽位写入后 padding 保持 0，字段值正确；
5. unsafe 指针槽位写入构成老→新引用，GC 后 `p.d` 仍指向新节点；
6. 只写 padding 字节不改变任何字段；写字段不改变 padding；
7. 嵌套 `outer{tag byte; inner padded; code uint16}` 的 inner 偏移 8、code 偏移 48、size 56；
8. 数组元素地址与 `unsafe.Add(base, k*Sizeof(padded))` 一致，步长等于 Sizeof；
9. 丢弃结构体基址，仅保留元素指针/字节视图，GC 后字段与 padding 仍正确，视图可重解释回结构体；
10. 只由 `paddedView` 返回的 []byte 保活的结构体，GC 后字段正确、指针字段为 nil；
11. 最后一行 `ALL PASS l07_struct_padding_alignment`。

# 失败判读

- 偏移/大小不符：字段布局或对齐计算错误（本用例只支持 64 位目标）；
- padding 非零：写入越界到 padding，或结构体复制未按布局进行；
- GC 后指针字段错乱：unsafe 指针槽位写入未触发写屏障；
- 视图重解释崩溃：字节视图未保活结构体，或长度/对齐错误；
- 仅 stress/k8s 失败：GOGC=1 下老→新引用未被标记。

# 参数

- `UAF_SEED`：决定 b/e 字段值、节点编号与数组元素下标，失败信息带种子；
- 布局断言依赖 linux/amd64、linux/arm64（64 位）。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_struct_padding_alignment/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
