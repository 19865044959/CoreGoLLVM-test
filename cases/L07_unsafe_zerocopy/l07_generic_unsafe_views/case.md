---
id: l07_generic_unsafe_views
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - sonic@v1.13.2: internal/rt/stubs_test.go:12 MakeSlice 对含指针/不含指针切片的零化语义
  - fiber@v2.52.6: utils/convert_test.go:44 UnsafeBytes string→[]byte 只读视图
  - pgx@v5.7.4: pgtype/bytea_test.go:86 PreallocBytes 保留原 cap、len 改写
tags: [L07, unsafe, generics, slice-header, unsafe.Slice, Sizeof]
requires: []
runtime: "<3s"
---

# 场景

泛型 unsafe helper：`gSlice[T]`、`gAt[T]`（用 `unsafe.Sizeof` 对 T 零值计算步长）、`gHeaderSlice[T]`（手工构造 `{data,len,cap}` 头）、`gBytesString[T ~byte]` 四个 helper 实例化为 byte、uint32、含 string/`*node`/[]byte 的 struct、泛型指针槽位 struct 与自定义字节类型。验证泛型实例化后的步长、元素地址、零拷贝 string 与跨 GC 保活。

## 期望行为

1. `gSlice`/`gAt` 对 byte/uint32/struct 的视图长度、元素地址与 `&arr[k]` 一致，地址差等于 `k*Sizeof(T)`；
2. `gHeaderSlice[byte]` 的 data/len/cap 正确，丢弃源数组后视图跨 GC 内容不变；
3. 含指针 struct 的泛型视图在丢弃源数组后，元素指针、string、`*node`、[]byte 字段全部正确；
4. `gslot[T]` 泛型指针槽位数组跨 GC 可读；
5. `gBytesString` 对 []byte 与自定义 `~byte` 类型都零拷贝（data 同址、源修改可见），复制 string 不相交；空输入返回空串；
6. 丢弃所有源切片、仅保留 string 视图与元素指针，GC 后内容仍正确；
7. 末行 `ALL PASS l07_generic_unsafe_views`。

## 失败判读

- 元素地址/步长不符：泛型 `Sizeof` 实例化错误或指针算术未按 T 宽度；
- GC 后视图失效：泛型构造的 slice/string 头未被当作 GC 根；
- `gBytesString` 不随源变化：转换意外复制；副本随源变化：复制被优化为别名；
- 泛型指针槽位解引用 SIGSEGV：槽位数组未被扫描；
- 仅 stress/k8s 失败：GOGC=1 并发标记下泛型派生视图存活问题。

## 参数

- `UAF_SEED`：决定数组长度、目标下标与 string/节点内容，失败信息带种子；
- 无 TORTURE 参数；仅在 Go 1.18+ 的泛型工具链上编译。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_generic_unsafe_views/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
