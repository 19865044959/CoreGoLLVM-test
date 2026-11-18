---
id: l07_byte_alias_overlap
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - gin@v1.10.0: internal/bytesconv/bytesconv_test.go:28 []byte→string 零拷贝别名与复制版对照
  - fiber@v2.52.6: utils/convert_test.go:19 UnsafeString 与 string() 的别名/复制语义
  - fasthttp@v1.59.0: args_test.go:150 PeekMulti 返回共享子切片
tags: [L07, unsafe, alias-detection, byte-range, append-realloc, GC]
requires: []
runtime: "<3s"
---

# 场景

字节别名检测：用 `unsafe.SliceData` 的 data 指针对区间做相交判断（uintptr 只在单个返回表达式中出现），并与实际写可见性对照——重叠子切片、相邻/不相交区间、显式复制、cap 内 append、满 cap 重新分配、`unsafe.String` 视图、复制 string、手工 `{data,len}` string 头，以及丢弃源变量后仅保留视图/全局 header 的 GC 场景。

## 期望行为

1. 重叠子切片、子切片与父切片、cap 内 append 结果判为别名；相邻、不相交、空视图、nil、显式副本判为非别名；
2. 写源切片时，别名副本观察到变化，显式副本不变；cap 内 append 写入对兄弟视图可见，重新分配的 append 写入隔离；
3. `unsafe.String` 视图与源 []byte 判为别名，写源可见；`string(view)` 副本判为非别名且内容相等；
4. 手工 header 构造的 string 与直接 `unsafe.String` 同址、内容一致；
5. 丢弃源变量并 GC 后，保留的 []byte/string 视图、全局 header 仍互相别名，内容与写可见性正确；
6. 末行 `ALL PASS l07_byte_alias_overlap`。

## 失败判读

- 别名判断与实际写可见性不一致：data/len 计算错误，或 append/copy 语义被误解；
- 副本随源变化：复制语义被错误优化为别名；
- GC 后视图失效/efence 崩溃：视图未让底层数组存活；
- 全局 header 重建内容不符：string 头布局或指针位图问题；
- uintptr 跨安全点导致的偶发失败：实现违反单表达式约束。

## 参数

- `UAF_SEED`：决定缓冲长度（128..159）与子切片起点，失败信息带种子；
- 无 TORTURE 参数；偏移按 64 位指针宽度计算。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_byte_alias_overlap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
