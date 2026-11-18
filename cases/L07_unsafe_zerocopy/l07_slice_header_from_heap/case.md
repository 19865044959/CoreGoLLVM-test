---
id: l07_slice_header_from_heap
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - goccy-json@v0.10.5: path_test.go:12 路径抽取返回输入的子切片，依赖堆数组生命周期
  - sonic@v1.13.2: ast/node_test.go:137 惰性节点保存输入子切片，Load 前不物化
tags: [L07, unsafe, unsafe.Slice, slice-cap, append, write-barrier]
requires: []
runtime: "<3s"
---

# 场景

从 `new([32]rec)` 堆数组用 `unsafe.Slice(&arr[0], 32)` 构造 len=cap=32 的切片，丢弃数组变量后由切片头保活；元素含 string/`*node`/`[]byte`。分别验证 cap 内 append（原地写堆数组）、cap 用尽 append（重新分配且不改源）、三索引切片钉死 cap 后 append，以及跨 GC 后所有指针字段内容。

# 期望行为

1. 数组变量置 nil 后 GC，32 个基础元素及其 link/data 全部正确；
2. `view[:8]` cap 为 32，append 后 len=9 且 `view[8]` 可见新元素；
3. `unsafe.Slice(&arr[0], 4)` 的 cap=4，append 必须重新分配，`view[4]` 保持原值；
4. `view[10:12:12]` append 必须重新分配，`view[12]` 保持原值；
5. 继续在 cap 内 append 多元素并 GC，新元素指针字段有效；
6. 从 slice 基址做 `unsafe.Add` 单表达式指针算术，定位到第 8 个元素；
7. 最后一行 `ALL PASS l07_slice_header_from_heap`。

# 失败判读

- GC 后基础元素错乱：slice 头未保活堆数组，或元素扫描位图错误；
- append 越界改写源数组：cap 计算错误（把 cap 当成 len 或反之）；
- 新元素字符串/指针失效：append 写屏障缺失，老对象引用新对象未被标记；
- 指针算术目标不一致：元素宽度/偏移计算错误。

# 参数

- `UAF_SEED`：决定 cap 内追加的额外元素数 `4+seed%5`，失败信息带种子；
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_slice_header_from_heap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
