---
id: l13_overlap_copy
dim: L13_runtime_ds
priority: P0
sources:
  - sonic@v1.13.2: ast/buffer_test.go:42 Test_linkedPairs_Push 分块节点缓冲 Push/Pop 后的重排/重接
  - badger@v4.5.0: y/y_test.go:44 TestPageBuffer 跨页缓冲写入时在页边界上的拷贝
tags: [L13, copy, overlap, memmove, pointer-slice, write-barrier, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

`copy` 在同底层数组重叠区间上的 memmove 语义，覆盖指针切片与字节切片：

1. `[]int`（256 元素）前移/后移/相交/全等/贴边 10 组偏移，用“临时副本 + 逐元素赋值”的独立参考实现对照内容与返回值（min(len)）；
2. 全等自拷贝保持内容；nil dst/src 与零长 dst 返回 0 且不修改；
3. `[]*cell`（128 个含 id/name/pad 的对象）5 组重叠搬移，GC 后逐个校验对象内容；自拷贝保持指针身份；
4. 两个不同底层数组的切片之间非重叠拷贝；
5. `[]string`（96 个）4 组重叠搬移，GC 前后内容一致；
6. 256KB `[]byte` 7 组重叠搬移（含 100000/50000 交叉、贴边 100B），GC 前后与参考实现逐字节一致；
7. `UAF_SEED` 驱动的 300 次随机 `[]*cell` 重叠拷贝（随机 dst/src 偏移），用参考模型维护期望 id 序列，每 50 次显式 GC + 垃圾分配并全量校验；统计至少发生一次真实区间相交。

# 期望行为

1. 每组 int/byte 拷贝的内容与独立参考一致，返回值为 `min(len(dst),len(src))`；
2. GC 后 int/byte/string 重叠拷贝结果不变；
3. 指针重叠搬移后每个槽位都是非 nil 且 id/name/pad 与模型一致，GC 后仍一致；
4. `copy(s,s)` 后指针身份不变；
5. 随机 300 次操作后模型与所有 cell 一致，且至少有一次操作满足 dst/src 区间相交且偏移不同；
6. 最后一行 `ALL PASS l13_overlap_copy`。

# 失败判读

- 内容与参考不一致：copy 未按 memmove 语义处理重叠（前向覆盖），或偏移计算错误；
- 返回计数错误：长度 min 计算错误；
- GC 后指针槽为 nil 或字段乱码：重叠搬移后的指针未被写屏障记录/对象被提前回收（UAF）；
- 自拷贝后指针身份变化：copy 被错误实现为逐元素清空或重新分配；
- 随机路径模型不一致：某次偏移的源区间在拷贝中被提前覆盖（预期语义错误）。

# 参数

- `UAF_SEED`：随机偏移序列与 256KB 字节内容的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_overlap_copy/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
