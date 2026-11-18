---
id: l09_cgo_pure_calls
dim: L09_cgo
priority: P1
sources:
  - go-ethereum/containerd/tailscale: cgo 调用路径（参见 docs/SOURCES.md 中 L09 标注）
tags: [L09, cgo, C-call, pointer-liveness]
requires: [cgo]
runtime: "<3s"
---

# 场景

通过 cgo 调用静态 C 函数做整数混合运算，并在循环中调用；Go 侧对象（含指针字段）跨 cgo 调用和 GC 后仍然有效。覆盖 cgo 代码生成、C 调用约定、调用点是 GC 安全点。

# 期望行为

1. 300 轮 `c_mix` 结果与 Go 侧公式一致；
2. 用 Go 对象字段调用 C 后 GC，对象内容不变；
3. 50 次 cgo 循环调用后对象仍正确；
4. 最后一行 `ALL PASS l09_cgo_pure_calls`。

# 失败判读

- 结果错误：cgo 参数/返回值转换或 C 编译器代码生成问题；
- GC 后对象损坏：cgo 调用点的栈映射缺失；
- 构建失败：`CGO_ENABLED=0`、无 C 工具链，或编译器不支持 cgo（用 `CAPS=cgo` 过滤）。

# 参数

需要 `CGO_ENABLED=1` 和 gcc/clang；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_pure_calls
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && /tmp/case
```
