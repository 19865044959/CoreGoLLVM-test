---
id: l13_string_intern_table
dim: L13_runtime_ds
priority: P2
sources:
  - sonic@v1.13.2: internal/caching/pcache_test.go:26 TestPcacheRace 按类型缓存并复用同一编译结果（规范化指针复用）
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 TestStringReferring 解码 string 与输入 buffer 的别名/拷贝关系
tags: [L13, string, intern, canonical-pointer, map, purge, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

实现 `map[string]*string` 的字符串 intern 表：同内容字符串只保留一个规范 `*string`，重复 intern 必须返回同一指针。每轮（默认 3 轮）从 5 字母表生成长度 0..12 的 3000 个字符串（大量重复），并加入空串、中文、emoji、混合串；覆盖：

1. 规范指针复用与 add/hit 计数；
2. 源 `[]byte` 改写后规范串内容不变（转换拷贝语义）；
3. GC 后所有规范指针/内容仍有效；
4. 按 `len(k)%4==0` 清理条目（含空串），剩余条目跨 GC 有效，被清理键确实不存在；
5. 重新 intern 被清理内容后表规模恢复、内容正确；
6. 排序后逐项校验 `key == *value`（全表自洽）；
7. 8KB 大字符串与空串的规范化（不同来源构造必须得到同一指针）。

每轮至少 3 次显式 GC + 垃圾分配；map 遍历只用于计数/清理，输出校验全部走排序 key。

# 期望行为

1. 初始 intern 后表大小等于内容去重集合大小，adds 等于唯一数，二次全量 intern 后 hits == 2*len(contents)-unique；
2. 每次 intern 返回的指针解引用等于内容；同内容再次 intern 返回同一指针；
3. 源 buffer 改写后，规范串内容与指针不变；
4. GC 后 ptrs、表内条目与排序 key 全部自洽；
5. purge 返回数量与模型统计一致，purge 后表大小正确，被清理键不在表中；
6. 重新 intern 后表大小恢复，8KB/空串跨 GC 正确且同内容同指针；
7. 最后一行 `ALL PASS l13_string_intern_table`。

# 失败判读

- 同内容返回不同指针：规范表查找/插入路径错误；
- 源 buffer 改写后内容变化：string 与可变 []byte 共享底层数组（别名 bug）；
- GC 后 `*value` 乱码或 key 查不到：map value 指针或 string 数据未被扫描/保活；
- purge 后仍有被清理键：删除路径错误；
- 大字符串路径失败：大对象分配/存活问题。

# 参数

- `UAF_SEED`：每轮字符串内容与长度的种子，失败输出带该种子；
- `TORTURE_ROUNDS`：轮数，默认 3，范围 1..200。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_string_intern_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=50 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
