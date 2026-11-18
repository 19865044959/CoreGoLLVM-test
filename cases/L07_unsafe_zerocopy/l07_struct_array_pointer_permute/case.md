---
id: l07_struct_array_pointer_permute
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: ast/buffer_test.go:42 节点块/元素链表 Push 按步长重接 next 指针
  - goccy-json@v0.10.5: decode_test.go:2947 两次解码比较指针字段地址，验证复用 vs 重新分配
  - badger@v4.5.0: table/merge_iterator_test.go:162 多路有序记录归并，依赖稳定的元素定位
tags: [L07, unsafe, pointer-arithmetic, struct-array, permutation, GC]
requires: []
runtime: "<10s"
---

# 场景

struct 数组上的指针算术与指针置换：用单表达式 `unsafe.Add(base, k*Sizeof(rec))` 把
`*[N]rec` 的元素地址收进 `[]*rec`，再对指针切片排序、反转、旋转；原数组值顺序保持不变。
随后丢弃数组基址与 `unsafe.Slice` 视图，仅靠元素指针跨 GC 保活，最后只保留一个内部元素指针，
用单表达式 uintptr 减法反推数组基址与物理邻居。元素含 string、`*leaf` 与 []byte，用于验证
数组扫描位图。

## 期望行为

1. 每个 GEP 元素指针等于 `&arr[i]`，`unsafe.Slice` 视图元素地址也一致；
2. 元素地址差等于 `k*Sizeof(rec)`，物理邻居（+/- stride）的 id 连续；
3. 指针切片按键排序后覆盖每个元素且无重复，原数组顺序不变；
4. 反转与按 `UAF_SEED` 旋转后的 id 序列与期望一致；
5. 丢弃数组/视图、仅保留 `[]*rec` 并 GC 后，元素字段、string、leaf、data 全部正确；
6. 只保留单个内部元素指针并 GC 后，仍可读取该元素、物理邻居，并能反推出 id==0 的基址；
7. uintptr 只在单个表达式内出现，不跨安全点保存；
8. 末行 `ALL PASS l07_struct_array_pointer_permute`。

## 失败判读

- GEP 地址不相等：步长/偏移计算错误；
- 排序后原数组被改动：误用了元素指针替换而非指针排序；
- GC 后字段错乱/efence 崩溃：元素指针或内部指针未被视作整数组根；
- 反推基址 id 非 0：字段偏移或 stride 计算错误；
- 仅 stress/k8s 失败：并发标记下派生指针/数组扫描问题。

## 参数

- `UAF_SEED`：决定数组长度（16..20）、值模式与旋转量，失败信息带种子；
- 无 TORTURE 参数；布局断言依赖 64 位目标。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_struct_array_pointer_permute/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
