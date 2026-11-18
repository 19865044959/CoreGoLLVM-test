---
id: l13_slice_of_slices
dim: L13_runtime_ds
priority: P1
sources:
  - pgx@v5.7.4: pgtype/array_test.go:8 TestParseUntypedTextArray PostgreSQL 多维数组解析出的嵌套切片
  - sonic@v1.13.2: issue_test/issue762_test.go:15 TestIssue762 解码含 [][2]string 的小对象
tags: [L13, slice-of-slices, jagged, aliasing, insert-delete, clone, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

锯齿形二维切片 `[][]*cell`（64 行，行长为 0..9，cell 含 id/name/data）与 `[][]byte` 块视图。覆盖：

1. 构建后 GC，按行逐个校验长度与 cell 内容；
2. 向第 13 行追加 3 个 cell，只影响该行；
3. 第 10 行使用额外容量构建，取 `[1:]` 子切片：写 `sub[0]` 对父行可见；`append(sub, x)` 不改变父行长度（若还有容量，只写父行可见长度之外）；
4. 在第 5 行插入新行（`append` + 重叠 `copy` 搬移行头），校验所有行；
5. 删除第 7 行（重叠 copy 前移 + 尾部置 nil）；
6. 行级过滤：只保留奇数长度的行，原地压缩并清零尾部；
7. 浅拷贝 `[][]*cell` 共享行底层数组；深拷贝只复制行头和元素指针，替换深拷贝的行槽不影响父行；
8. `[][]byte`：8 个 512B 块用三索引切片限定容量共享同一 4KB 底层数组，验证相邻块不重叠、行拷贝独立、满容量 append 不改原行长度；
9. 每个阶段后显式 GC + 垃圾分配并重新校验。

# 期望行为

1. 每次变更后行数、行长、cell id/name/data 与模型 `[][]int` 一致；
2. 追加只改变目标行；子切片写对父行可见，append 不改变父行长度；
3. 插入/删除行后行序正确，GC 后所有 cell 有效；
4. 行过滤只保留奇数长度行，尾部被置 nil，过滤后 GC 内容一致；
5. 浅拷贝行槽修改对父行可见；深拷贝行槽修改对父行不可见，且跨 GC 保持；
6. 8 个 byte 块内容互不影响，行拷贝修改不回写原块，append 后原块长度仍为 512，GC 后 8 块内容与初始一致；
7. 最后一行 `ALL PASS l13_slice_of_slices`。

# 失败判读

- 行/单元格 id 或内容错位：插入/删除时的重叠 copy 边界或模型更新错误；
- 子切片 append 改了父行长度：对 cap/len 的假设与实际不符；
- 浅拷贝不共享行底层数组：拷贝实现错误（本用例按共享语义判定）；
- 深拷贝修改回写到父行：切片头未复制；
- `[][]byte` 相邻块互相影响或 append 越界写：三索引切片容量限制或共享底层数组判断错误；
- GC 后 cell 数据乱码：嵌套切片中的指针未被扫描（UAF）。

# 参数

- `UAF_SEED`：行长分布与 byte 块内容的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slice_of_slices/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
