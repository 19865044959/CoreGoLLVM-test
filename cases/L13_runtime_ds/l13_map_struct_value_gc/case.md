---
id: l13_map_struct_value_gc
dim: L13_runtime_ds
priority: P1
sources:
  - badger@v4.5.0: manifest_test.go:249 `map[uint64]TableManifest` 以结构体值承载 manifest
  - caddy@v2.10.0: admin_test.go:379 `originalModules := make(map[string]ModuleInfo)` 结构体值快照
  - etcd@v3.5.0: client/pkg/types/urlsmap_test.go:29 `map[string]URLs` 结构体值聚合
tags: [L13, map, struct-value, value-copy, slice-sharing, pointer-field, overwrite, delete, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟配置/清单层以结构体值作为 map value 的路径（badger manifest 表、caddy 模块配置快照、etcd URL 映射）。value 是含 `string`、嵌套 struct、`[]int64` 与 `*inner` 的结构体：

1. 建立 1400+ 项 `map[string]svValue`；对其中一个 value 做局部改写，验证标量/字符串/指针头按值复制，而 slice 字段仍共享底层数组；
2. 每 5 项整体覆盖为新结构体值，校验新 id/指针；
3. 每 7 项删除，GC 后确认缺席；再以不同 id 重插；
4. 扩容到 2500 项，GC 后全量重算 name/blob/list/ptr 内容；
5. 遍历聚合 `id%1000` 并与模型比较。

关键位置均显式 `runtime.GC()` 并制造垃圾，验证 map 桶内结构体值的指针字段被正确扫描。

# 期望行为

1. 结构体 value 的写入/读取按值复制；修改局部副本的标量字段不影响 map；
2. slice 字段共享底层数组，局部写入对 map 内值可见（Go 值语义的浅拷贝）；
3. 覆盖、删除、重插、扩容后长度和内容模型一致；
4. GC 后 value 内的 string/slice/指针字段仍有效，无 UAF；
5. 最后一行输出 `ALL PASS l13_map_struct_value_gc`。

# 失败判读

- `scalar fields copied by value` 失败：map value 被错误按引用存储；
- `slice field did not share backing`：slice 头被深拷贝，违背值语义；
- `content mismatch`：GC 后桶内结构体的指针字段损坏或桶迁移丢值；
- `iteration aggregate` 不符：覆盖/删除后 map 内容与模型不一致。

# 参数

- `UAF_SEED`：项数、id 偏移、salt 与遍历顺序的种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_struct_value_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
