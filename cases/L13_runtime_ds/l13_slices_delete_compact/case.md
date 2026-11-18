---
id: l13_slices_delete_compact
dim: L13_runtime_ds
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/golinters/gocritic/gocritic_test.go:50 `slices.Delete(result, i, i+1)` 循环删除检查项
  - golangci-lint@v2.0.2: pkg/golinters/gocritic/gocritic_test.go:57 `slices.Compact(slices.Sorted(slices.Values(in)))` 去重
  - nats-server@v2.11.0: server/jetstream_cluster_4_test.go:3361 `slices.DeleteFunc` 过滤服务器列表
tags: [L13, slices, delete, compact, replace, deletefunc, grow, pointer, tail-reuse, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟调用链清理/去重时对 `[]*Item` 使用标准库 `slices` 组合：

1. `slices.Delete(s, 100, 300)`：中段删除后前后段指针顺序不变；
2. `slices.Compact`：连续重复指针只保留每组第一个；
3. `slices.DeleteFunc`：按 key/seq 谓词删除，剩余顺序与模型一致；
4. `slices.Replace(s, 50, 60, extras...)`：中段替换为新的指针元素；
5. `slices.Grow` + 逐个 append：长度不变、容量达标，旧前缀指针不变；
6. 删除后立即追加：尾段复用不得复活已删除元素。

每阶段之间执行显式 `runtime.GC()`，校验原有对象 payload 与新增元素均完整。

# 期望行为

1. Delete/DeleteFunc/Compact/Replace 的长度与指针序列与模型逐一相等；
2. Grow 不改变长度和内容，容量至少增加请求值；
3. 删除后的尾段被新元素安全覆盖，旧元素不重现；
4. GC 后所有仍被 slice 引用的对象内容有效；
5. 最后一行输出 `ALL PASS l13_slices_delete_compact`。

# 失败判读

- `slices.Delete: prefix/suffix`：位移长度或拷贝方向错误；
- `slices.Compact` 失败：相邻相等判断作用于错误元素；
- `slices.Replace: composition`：替换区间边界计算错误；
- `tail reuse`：删除后的尾槽残留旧指针并影响追加；
- GC 后 payload 损坏：指针元素漏扫（UAF）。

# 参数

- `UAF_SEED`：Replace 插入个数、Grow 容量与 salt 的种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slices_delete_compact/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
