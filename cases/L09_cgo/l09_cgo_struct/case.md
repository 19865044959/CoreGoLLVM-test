---
id: l09_cgo_struct
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09；C struct 边界是常见 cgo 路径）
tags: [L09, cgo, struct, sizeof, offsetof, layout]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定 C 结构体的常规场景：`point_t {int32 x; int32 y; int64 z; char tag[8]}`。C 侧填充、Go 侧直接读字段；Go 侧改字段、C 侧求和；并把 Go 的 `[]C.point_t` 传给 C 批量读取。同时用 C 的 `sizeof`/`offsetof` 校验 Go `unsafe.Sizeof/Offsetof` 布局一致。

# 期望行为

1. `unsafe.Sizeof(C.point_t{})` 与 C `sizeof(point_t)` 一致，`z`/`tag` 的 offset 与 C 一致；
2. C 填充后 Go 直接读到的 x/y/z/tag 正确，C 求和正确；
3. GC 后字段与 tag 不变；
4. Go 直接写字段后 C 求和立即反映新值；
5. Go 的 16 元素 `[]C.point_t` 传给 C 求和结果正确，GC 后重复调用一致；
6. 最后一行 `ALL PASS l09_cgo_struct`。

# 失败判读

- sizeof/offset 不一致：cgo 类型映射或对齐假设错误；
- tag 乱码：C 侧未写结尾 NUL，或 Go 读取越界；
- GC 后字段损坏：Go 数组指针未被保持（缺少 KeepAlive）；
- 崩溃：C 保留了 Go 指针越过调用期（本用例 C 只在调用内读取）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_struct
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
