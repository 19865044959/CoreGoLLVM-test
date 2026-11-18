---
id: l13_maps_clone_equal
dim: L13_runtime_ds
priority: P1
sources:
  - tailscale@v1.80.2: util/osdiag/osdiag_windows_test.go:106 `maps.Clone`，:110-112 连续 `maps.Copy`
  - tailscale@v1.80.2: clientupdate/clientupdate_test.go:633 `maps.Equal` 比较解包前后文件映射
  - golangci-lint@v2.0.2: pkg/golinters/gocritic/gocritic_test.go:47-57 `slices.Clone`/`slices.Delete`/`slices.Compact` 组合
  - quic-go@v0.50.1: internal/ackhandler/sent_packet_handler_test.go:1310 `slices.Compact` 对 ACK 序号去重
tags: [L13, maps, slices, clone, shallow-copy, equal, delete-func, compact, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

标准库 `maps`/`slices` 在指针值容器上的浅拷贝与增删语义：

1. `maps.Clone` 500 项 `map[string]*leaf`：value 指针相同；clone 增/删 key 不影响原表；修改共享对象双端可见；GC 后逐项校验；
2. `maps.Equal` 比较指针身份，`maps.EqualFunc` 逐内容比较；结构等值但重新分配的对象使 Equal=false、EqualFunc=true；nil 与空 map 相等；
3. `maps.Copy`：重叠 key 被覆盖为源指针，dst 独有 key 保留，空源 no-op；
4. `maps.DeleteFunc` 删除 id%3==0，计数与成员正确；`maps.Keys` 收集后排序校验；
5. `slices.Clone` 浅拷贝：替换元素不影响原切片，修改对象双端可见；
6. `slices.Compact` 连续去重、`slices.Delete` 中间删除（尾部清零）、`slices.Insert`/`Replace` 移位、`slices.Grow` 容量与长度、`slices.SortFunc` 排序；
7. 每个阶段显式 GC，最终校验所有 map/切片中的 leaf 指针与内容。

# 期望行为

1. Clone 是浅拷贝：指针相同、容器独立；
2. Equal/EqualFunc 在指针身份与内容两种语义下结果正确；
3. Copy/DeleteFunc/Keys 的覆盖、删除与集合结果正确；
4. slices 的 Clone/Compact/Delete/Insert/Replace/Grow/Sort 结果与参考一致，Delete 尾部被清零；
5. GC 前后所有容器内容不变；
6. 最后一行 `ALL PASS l13_maps_clone_equal`。

# 失败判读

- `clone value pointer changed`：Clone 深拷贝了 value（与标准库语义不符）或丢值；
- `Clone add/delete leaked`：clone 与原 map 共享桶；
- `Equal ignored` / `EqualFunc content`：比较函数语义错误；
- `Delete did not zero tail`：删除后旧指针仍被底层数组保活；
- `sort/insert/replace`：切片移位边界错误；
- GC 后 payload 乱码：容器元素指针未被扫描（UAF）。

# 参数

- `UAF_SEED`：value 内容与打乱顺序种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_maps_clone_equal/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
