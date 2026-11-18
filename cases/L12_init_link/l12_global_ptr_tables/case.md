---
id: l12_global_ptr_tables
dim: L12_init_link
priority: P1
sources:
  - caddy@v2.10.0: modules_test.go:22 包级 map[string]ModuleInfo 全局模块表
  - gorm@v1.30.0: callbacks/create_test.go:14 包级 var schemaCache = &sync.Map{}
  - go-redis@v9.8.0: main_test.go:56 包级 clusterScenario 启动期构造 map/slice
tags: [L12, global-table, map, slice, pointer-identity, cross-reference, gc, concurrency]
requires: []
runtime: "<10s"
---

# 场景

包级变量 `tables` 在包初始化阶段由 `buildTables(64)` 构造：`map[int]*entry`、`[]*entry`、`map[string][]*entry` 分组表、`map[*entry][]*entry` 指针键交叉表、`[]map[string]*entry` payload 表以及单向链表。主程序向所有表插入新条目、修改 score，然后并发只读并穿插 GC，校验跨表指针身份与链表完整性。

# 期望行为

1. 初始 64 项：`byID`、`ordered`、`payloads` 长度一致，`byID[e.id]==e`、`payloads[i][e.name]==e`；
2. 每个 entry 的 payload 64 字节符合 `byte(id*7+i)`；内置 entry 的 id/name/score 符合构造公式；
3. 四个分组各 16 项，成员恰出现在 `id%4` 对应组中，组大小之和等于表大小；
4. 指针键交叉表每项有 2 个别名，内置项别名指向 `(i+1)%64` 与 `(i+7)%64`，插入项别名指向 `ordered[0]/ordered[1]`；
5. 链表从 `ordered[0]` 起长度等于表大小，`tail` 为最后一个元素；
6. 插入 1–3 个新 entry 后，所有表同步增长，score 统一 +100，校验仍通过；
7. 8 goroutine 并发读取五张表 + 主 goroutine GC 期间无错误；GC 后根指针、插入项指针与 payload 仍有效；
8. 最后一行 `ALL PASS l12_global_ptr_tables`。

# 失败判读

- `byID identity`/`payload table identity` 失败：包级 map/slice 中的指针被错误复制或重定位；
- 分组/链表长度错误：初始化期构造不完整或插入未同步到所有表；
- GC 后 payload 或交叉表损坏：全局指针表扫描/写屏障错误；
- 并发阶段失败：只读阶段出现了隐藏写入；
- 失败行 `seed=` 可用于复现插入数量与遍历起点。

# 参数

- `UAF_SEED`：决定插入条目数量（1–3）、插入 id 基值与并发遍历起点；全量校验与起点无关。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_global_ptr_tables/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
