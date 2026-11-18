---
id: l07_global_pointer_table
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - go-redis@v9.8.0: internal_test.go:86 TestRingSetAddrsAndRebalanceRace 全局分片表并发替换 shard 指针
  - fasthttp@v1.59.0: userdata_test.go:124 TestUserData_GC 全局 user data map 中的值在 Reset 前保持存活
  - pgx@v5.7.4: pgxpool/pool_test.go:111 连接池表 Acquire/Release 生命周期
tags: [L07, unsafe, global-root, pointer-table, map, slice, GC]
requires: []
runtime: "<10s"
---

# 场景

全局 `unsafe.Pointer` 注册表是配置缓存、连接表、池化对象索引的常见结构。用例使用包级 `[32]unsafe.Pointer` 表 + `map[string]unsafe.Pointer` + 全局 `[]unsafe.Pointer`，对象只经全局槽位可达，覆盖：填表后 GC、map 小容量起步多次扩容并穿插 GC、删除 key 后 GC、槽位释放/复用代际、全局 slice append 扩容/截断，以及全部清空后类型化引用保活。

# 期望行为

1. 包级指针表填满 n 个槽位后 GC，每个对象的 id/name/tags/link 全部正确，首尾槽位非 nil；
2. `map[string]unsafe.Pointer` 从 cap=4 扩容到 96+ 项，插入期间每 16 项 GC 一次，最终 len 正确、每项 GC 后内容正确；
3. 删除（i%3==0）的 key 查询为 nil，未删除项 GC 后内容正确；
4. 被删除对象的类型化引用在 GC 后仍可读；
5. 包级表按 `i%4==0` 释放槽位后 nil 且代际 +1，GC 后旧对象（typed ref）仍有效；复用槽位后代际为 3，GC 后新对象正确、旧对象仍有效；
6. 全局 `[]unsafe.Pointer` append 到 64 项（期间 GC）后所有对象正确；截断到 10 项后，尾部对象由 typed ref 在 GC 后保活，头部 10 项仍正确；
7. 清空表、map 与 slice 后 GC 不崩溃，所有保留的类型化引用仍逐字段正确；
8. 最后一行 `ALL PASS l07_global_pointer_table`。

# 失败判读

- GC 后条目错乱/efence 崩溃：包级 unsafe.Pointer 数组或 map 值未被扫描，对象被提前回收；
- map 扩容期间丢失条目：map 值写入缺写屏障或扩容搬迁错误；
- 删除后仍能查到：delete 未生效或旧值残留；
- 槽位复用代际不符：全局表释放/复用状态机错误；
- 截断 slice 后尾部对象失效：typed ref 未被栈位图标记；
- 仅 stress/k8s 失败：GOGC=1 下全局根与并发标记问题。

# 参数

- `UAF_SEED`：决定表项数（12..20）、map 键前缀与条目数（96..128）、标签，失败信息带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_global_pointer_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
