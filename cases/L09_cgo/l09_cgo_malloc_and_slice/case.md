---
id: l09_cgo_malloc_and_slice
dim: L09_cgo
priority: P1
sources:
  - go-ethereum: cgo secp256k1/大整数路径（场景表待 Phase 4 补充）
tags: [L09, cgo, malloc, Go-pointer-to-C]
requires: [cgo]
runtime: "<3s"
---

# 场景

两类典型 cgo 边界：Go 数值切片指针传给 C 读取（C 不保留）；C.malloc 分配、C 填充、Go 读取，期间 GC；以及 50 轮 malloc/free + GC。

# 期望行为

1. C 读取 Go 切片求和为 15，GC 后切片不变；
2. C 分配 64 个 int64，C 填充后 GC，Go 求和为 `64*1000 + 64*63/2`；
3. 50 轮 C 内存写入 + GC 后读回值一致；
4. 最后一行 `ALL PASS l09_cgo_malloc_and_slice`。

# 失败判读

- 求和错误：C 读取越界/参数转换错误；
- GC 后 C 内存内容变化：C 内存被误当作 Go 堆扫描（cgo 指针规则）；
- 崩溃：Go 指针被 C 保留后 GC 移动/回收。

# 参数

`CGO_ENABLED=1` + gcc；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_malloc_and_slice
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
