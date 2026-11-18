---
id: l05_copy_growth_overlap
dim: L05_alloc_gc
priority: P1
sources:
  - pgx@v5.7.4: internal/pgio/write_test.go:23 的 TestAppendUint16BufWithCapacityDoesNotAllocate：cap 足够时 append 不扩容
  - fasthttp@v1.59.0: round2_64_test.go:10 的 TestRound2ForSliceCap：切片容量向上取整
tags: [L05, cap-growth, copy, overlapping-copy, pointer-elements, clone, gc]
requires: []
runtime: "<10s"
---

# 场景

含 id/name/[]byte/*node 的 struct 切片从 cap=4 起步，append 512 项触发多次
扩容；扩容前保留旧底层数组视图，扩容后 GC 校验旧前缀不变。随后做三段重叠
`copy`（前向/后向/整段移动）、复制到新切片、`slices.Clone`，再触发一次扩容。
每步用独立的 id 模型对照，并在垃圾分配 + 显式 GC 后校验指针身份与内容。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_copy_growth_overlap`；
2. 初始 append 至少发生 5 次容量变化，元素 id/name/data/ptr 全部正确；
3. 超过 cap 的 append 更换底层数组，旧数组前缀跨 GC 后保持原值；
4. 三段重叠 copy 的返回长度与 `copy` 的 memmove 语义一致，模型序列逐项匹配；
5. 复制到新切片保留元素内 `*node` 指针身份，新切片与原切片不共享元素存储；
6. `slices.Clone` 后改写原切片字段不影响克隆，克隆跨 GC 仍正确；
7. 第二次扩容后旧快照不变，最终丢弃原切片，dst/clone 仍有效；
8. 在 base/clobberfree 等 profile 下结果一致。

## 失败判读

- `append beyond cap did not reallocate`：growslice 容量语义错误；
- `id=... want=` / `len=`：重叠 copy 未按 memmove 处理或元素移动错位；
- `pointer identity lost`：复制/扩容后指针字段丢失或被复制成错误对象；
- `clone shares mutable struct field`：Clone 退化为浅别名；
- `old-backing ... +GC`：旧底层数组被提前回收或扩容时被覆写；
- `data first byte` / `name=`：元素数据在增长/复制中损坏。

## 参数

- `UAF_SEED`：决定每个元素的 data 长度与字节内容（默认 1，解析失败回退 1），失败输出带种子；
- 无 TORTURE 参数；固定 512 初始元素 + 128 追加元素 + 一次额外扩容。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_copy_growth_overlap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
