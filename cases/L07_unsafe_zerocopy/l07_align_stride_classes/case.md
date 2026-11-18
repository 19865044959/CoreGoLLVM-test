---
id: l07_align_stride_classes
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - goccy-json@v0.10.5: size_test.go:10 64 位下结构体大小与字段布局断言
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 unsafe 拆解结构体/接口头与字段偏移
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 重解释为 string
tags: [L07, unsafe, alignment, padding, struct-layout, interface-field]
requires: []
runtime: "<10s"
---

# 场景

内存对齐/padding 的多类型矩阵：1/2/4/8 字节对齐结构体分别含 uint8/uint16/uint32/uint64/`*leaf`/string/interface 字段，断言 Sizeof/Alignof/Offsetof；用字节视图检查内部与尾部 padding 在普通写、unsafe.Add+Offsetof 写之后保持为 0；数组步长等于 Sizeof，含指针与 interface 字段的元素只由元素指针/字节视图保活时跨 GC 正确。

## 期望行为

1. a1/a2/a4/a8/ca 的 size/align 与所有字段偏移（含嵌套 `ca.v8.p`/`ca.v8.i`）符合 64 位布局；
2. string 头与 empty interface 的宽度均为 16，interface 对齐为 8；
3. 字段地址按各自对齐值取模为 0，a4/a8 的 padding 区间全为 0；
4. 经 `unsafe.Add` + Offsetof 写 uint64/`*leaf`/string/interface 后字段正确，padding 保持 0；
5. `[4]a8`、`[5]a4` 的元素地址差等于 `k*Sizeof`，`unsafe.Add` GEP 与 `&arr[k]` 一致；
6. 清零一个元素后其指针/string/interface 字段为空、邻居不变；丢弃数组变量并 GC 后，元素指针、interface 中的 `*leaf`/string、字节视图与重解释均正确；
7. 嵌套 ca 的 a4/a8 字段跨 GC 正确；
8. 末行 `ALL PASS l07_align_stride_classes`。

## 失败判读

- 偏移/大小不符：字段布局或对齐规则理解错误（本用例仅 64 位）；
- padding 非零：写入越界到 padding，或结构体复制未按布局进行；
- 数组步长不符：元素宽度/对齐计算错误；
- GC 后 interface 中的指针失效：interface 数据字未被扫描，或字段写入缺写屏障；
- 视图重解释崩溃：字节视图未保活结构体或长度错误；
- 仅 stress/k8s 失败：GOGC=1 下老→新引用未被标记。

## 参数

- `UAF_SEED`：决定字段值、数组下标、清零元素与 interface 内容，失败信息带种子；
- 布局断言依赖 linux/amd64、linux/arm64（64 位）。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_align_stride_classes/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
