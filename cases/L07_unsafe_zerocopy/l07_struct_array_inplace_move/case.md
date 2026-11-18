---
id: l07_struct_array_inplace_move
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - sonic@v1.13.2: ast/buffer_test.go:42 节点块/元素链表按步长定位并重接 next 指针
  - goccy-json@v0.10.5: decode_test.go:2947 两次解码比较指针字段地址，验证复用 vs 重新分配
  - badger@v4.5.0: table/merge_iterator_test.go:162 多路有序记录归并，依赖稳定的元素定位
tags: [L07, unsafe, struct-array, in-place-move, pointer-arithmetic, write-barrier, GC]
requires: []
runtime: "<10s"
---

# 场景

struct 数组的原地移动：元素为含 `string`、`[]byte`、`*leaf` 的记录。用
`unsafe.Add(base, i*Sizeof(rec))` 取得元素地址，通过类型化指针做整体交换，实现三次反转左旋
和 `[2,n)` 区间重叠右移一格，再插入一条新记录。模拟 AST 节点块搬迁、JSON 解码器复用节点
槽位与 LSM 归并时元素重排的路径。移动后记录内的指针字段必须仍指向原对象，写屏障必须正确
记录老对象指向新对象的引用。

## 期望行为

1. 每个 `unsafe.Add`/`unsafe.Slice` 元素地址与 `&arr[i]` 完全一致，步长等于 `Sizeof(mvRec)`；
2. 左旋 k（三次反转）后逻辑顺序为 `(i+k)%n`，每条记录的 name/link/data/mark 自洽；
3. 重叠右移后 `[3,n)` 为原 `[2,n-1)` 的 id，2 号槽为新记录，无重复 id；
4. 指针切片反转后每个元素仍指向同一物理记录，GC 后内容完整；
5. 丢弃数组与视图，仅保留元素指针切片，显式 GC 后全部记录可读且指针字段正确；
6. 只保留单个内部元素指针时可反推基址、访问前后邻居；
7. 末行 `ALL PASS l07_struct_array_inplace_move`，失败退出码 1。

## 失败判读

- 顺序错乱：反转/移动循环边界或模型计算错误；
- link/data 挂死或值不符：struct 整体赋值未触发写屏障，或元素指针跨 GC 失效；
- 重复 id：重叠移动方向错误（从前往后会自我复制）；
- `SIGSEGV`/`found bad pointer`：GC 扫描到数组中间态的半移动指针或栈图漏标；
- 仅 clobberfree/stress 失败：被移动对象的旧槽位被零化后仍被访问。

## 参数

- `UAF_SEED`：决定数组长度 18..24、旋转量 k、新记录 id 与字节模式，失败信息带种子。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_struct_array_inplace_move/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
