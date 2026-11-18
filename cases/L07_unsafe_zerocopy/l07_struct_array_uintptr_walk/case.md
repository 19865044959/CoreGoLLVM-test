---
id: l07_struct_array_uintptr_walk
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: ast/buffer_test.go:42 节点块/元素链表按步长遍历，元素指针跨分配存活
  - goccy-json@v0.10.5: size_test.go:10 断言 64 位下结构体大小与字段布局
  - goccy-json@v0.10.5: decode_test.go:2947 两次解码比较指针字段地址，验证复用 vs 重新分配
tags: [L07, unsafe, pointer-arithmetic, uintptr, struct-array, field-pointer]
requires: []
runtime: "<10s"
---

# 场景

struct 数组上的指针算术：用单表达式 `unsafe.Pointer(uintptr(base) + k*unsafe.Sizeof(rec{}))` 前向/后向/以中间元素为基址遍历，元素含 string、`*leaf`、padding 与混合宽度字段。再保留 `*uint64`、`*string`、`**leaf`、`*uint8` 字段指针，丢弃数组变量后显式 GC，并从字段指针用单表达式反推原元素与邻居。

## 期望行为

1. 前向、后向、中间基址三种 GEP 得到的 `*rec` 都等于 `&arr[k]`，元素字段逐项正确；
2. 元素地址差与 `k*Sizeof(rec)` 一致，结构体 Offsetof 断言成立；
3. `unsafe.Add(unsafe.Pointer(&arr[k]), Offsetof(...))` 得到的字段指针等于类型化字段地址；
4. 丢弃数组变量、只保留字段指针并 GC 后，字段值、string 内容、leaf 指针及其内容仍正确；
5. 从 `*string` 字段指针反推元素与前后邻居，字段全部正确；
6. uintptr 只在单个表达式内出现，不保存跨安全点；
7. 末行 `ALL PASS l07_struct_array_uintptr_walk`。

## 失败判读

- GEP 地址不等于 `&arr[k]`：步长/结构体大小计算错误，或负偏移方向错误；
- GC 后字段内容错乱/efence 崩溃：字段指针未被当作整对象根，或数组元素扫描位图漏标 string/leaf；
- 字段指针不等于类型化地址：Offsetof 与布局不符；
- uintptr 跨安全点导致悬垂：单表达式要求被破坏；
- 仅 stress/k8s 失败：GOGC=1 并发标记下派生指针问题。

## 参数

- `UAF_SEED`：决定数组长度（18..22）、目标元素下标、中间基址与字段值，失败信息带种子；
- 无 TORTURE 参数；布局断言依赖 64 位目标。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_struct_array_uintptr_walk/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
