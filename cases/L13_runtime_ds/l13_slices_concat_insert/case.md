---
id: l13_slices_concat_insert
dim: L13_runtime_ds
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/golinters/gocritic/gocritic_test.go:43 `return slices.Concat(in, toInsert)` 插入修复片段
  - tailscale@v1.80.2: wgengine/router/router_linux_test.go:467 `rules = slices.Insert(rules, pos, newRule)` 规则插入
  - tailscale@v1.80.2: health/health_test.go:270 `slices.Concat` 合并依赖告警列表
tags: [L13, slices, concat, insert, replace, grow, aliasing, pointer, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟规则/检查项组合时使用 `slices.Concat/Insert/Replace/Grow`（golangci-lint 自动修复、tailscale 路由与健康依赖列表）：

1. `slices.Concat(a, b)`：结果长度与指针序列正确；改写结果槽位不得影响输入；
2. 空/nil 输入、链式 `Concat(Concat(a,b), extras)`；
3. `slices.Insert` 在头、中、尾插入 extras，校验前缀/插入段/后缀指针序列；
4. `slices.Replace(b, 10, 30, extras...)` 的中段替换组合；
5. `slices.Grow(g, 300+)`：长度与内容不变、容量达标，再逐个 append 新元素。

阶段间显式 `runtime.GC()`，校验所有输入与结果中的指针元素指向对象仍完整。

# 期望行为

1. Concat 返回独立切片头，改写结果槽位不影响输入；
2. Insert 三个位置的元素顺序与模型一致；
3. Replace 的前缀 + 插入段 + 后缀与模型一致；
4. Grow 后长度不变、容量至少增加请求值，追加前缀保持不变；
5. GC 后指针元素有效；最后一行输出 `ALL PASS l13_slices_concat_insert`。

# 失败判读

- `slices.Concat: result aliased input array`：Concat 错误复用输入底层数组；
- `slices.Insert/Replace` 失败：位移区间或长度计算错误；
- `slices.Grow: capacity not grown`：growslice 未满足容量请求；
- GC 后 payload 损坏：指针元素漏扫（UAF）。

# 参数

- `UAF_SEED`：Insert 位置、Grow 增量与 salt 的种子，失败输出带该值。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_slices_concat_insert/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
