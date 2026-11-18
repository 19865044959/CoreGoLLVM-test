---
id: l07_array_struct_fields_unsafe
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 unsafe 拆解结构体布局与字段偏移
  - sonic@v1.13.2: ast/node_test.go:137 惰性节点字段保存输入子切片
  - goccy-json@v0.10.5: size_test.go:10 64 位结构体 Sizeof/Offsetof 断言
tags: [L07, unsafe, struct-array, nested-field, array-field, write-barrier]
requires: []
runtime: "<10s"
---

# 场景

struct 数组的字段访问：`row` 内含嵌套 `attr`，`attr` 又含 `[3]string`、`[2][]byte`、`*leaf`，`row` 尾部还有 `[2]*leaf`。用例用 `unsafe.Add(unsafe.Pointer(ep), Offsetof(row{}.at)+Offsetof(attr{}.tags))` 等嵌套偏移定位数组字段，与类型化字段地址对照；经 unsafe 字段指针改写、清零元素、丢弃数组变量后只保留字段指针跨 GC 校验。

## 期望行为

1. 嵌套偏移定位的 `*[3]string`、`*[2][]byte`、`**leaf`、`*[2]*leaf`、`*string`、`*uint16` 与类型化字段地址完全一致；
2. 元素内数组字段初值正确，嵌套 `link` 与 `refs` 指针字段可读；
3. 经 unsafe 字段指针改写标量、string、slice、指针后，普通字段访问可见，其他元素指纹不变；
4. GC 后新值有效，被替换的旧 string/buffer/leaf 由类型化引用保活且可读；
5. 清零一个元素后其指针/string/slice 字段为空，邻居不变，旧值仍有效；
6. 丢弃数组变量、仅保留字段指针并 GC 后，可从字段指针反推元素，所有嵌套字段与指针槽位正确；
7. 末行 `ALL PASS l07_array_struct_fields_unsafe`。

## 失败判读

- 字段指针不等于类型化地址：嵌套 Offsetof 相加错误；
- 写字段后邻居被改：偏移或写入宽度错误（越界写）；
- GC 后新指针失效：unsafe 字段写入缺少写屏障；
- 清零后旧值不可读：清零误改其他内存，或旧值只被字段引用；
- 仅 stress/k8s 失败：GOGC=1 下写屏障/栈映射问题。

## 参数

- `UAF_SEED`：决定数组长度（9..13）、目标元素、清零元素与字段值，失败信息带种子；
- 布局断言依赖 linux/amd64、linux/arm64（64 位）。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_array_struct_fields_unsafe/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
