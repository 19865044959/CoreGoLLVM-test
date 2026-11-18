---
id: l13_maps_copy_deletefunc
dim: L13_runtime_ds
priority: P1
sources:
  - tailscale@v1.80.2: util/osdiag/osdiag_windows_test.go:106 `maps.Clone`，:110 `maps.Copy` 连续合并
  - tailscale@v1.80.2: net/netcheck/netcheck_test.go:711 `slices.Sorted(maps.Keys(plan))` 排序键
  - golangci-lint@v2.0.2: pkg/golinters/gocritic/gocritic_test.go:270 `slices.Collect(maps.Keys(...))` 收集键
tags: [L13, maps, copy, deletefunc, clone, equal, keys, values, shallow-copy, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟配置合并与诊断快照使用 `maps` 包组合（tailscale osdiag、netcheck、golangci-lint）：

1. `maps.Copy(dst, src)`：覆盖 dst 中重叠键，保留 dst 独有键；value 为指针，拷贝后指针身份不变；
2. 修改 src 指向的对象，dst 可见（浅拷贝），随后恢复；
3. `maps.DeleteFunc` 按 `id%7==0` 与额外键删除；
4. `maps.Keys` + `slices.Sorted` 校验键集合；`maps.Values` + `slices.Collect` 校验数量与 id 聚合；
5. `maps.Clone` 后删除不影响源；`maps.Equal` 对指针相等的 map 为真、替换一个 value 指针后为假；
6. nil map 上的 Copy（nil 源）、DeleteFunc、Clone、Equal 均不得 panic。

阶段间执行显式 `runtime.GC()`，校验源/目标 map 中指针 value 指向对象仍完整。

# 期望行为

1. Copy 后长度、键集合与指针身份正确，dst 独有键保留；
2. DeleteFunc 后仅谓词命中项消失；
3. Keys/Values 的集合与聚合值和模型一致；
4. Clone 浅拷贝、Equal 按指针比较；nil map 操作安全；
5. GC 后所有 value payload 有效；最后一行输出 `ALL PASS l13_maps_copy_deletefunc`。

# 失败判读

- `maps.Copy: pointer mismatch`：复制时深拷贝或丢键；
- `maps.DeleteFunc` 相关：谓词删除后长度/键集合错误；
- `maps.Keys/Values` 聚合不符：迭代器遗漏或重复；
- `maps.Equal` 失败：等价判断退化为内容比较或未比较指针；
- GC 后 payload 损坏：map value 指针漏扫（UAF）。

# 参数

- `UAF_SEED`：map 项数与 salt 的种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_maps_copy_deletefunc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
