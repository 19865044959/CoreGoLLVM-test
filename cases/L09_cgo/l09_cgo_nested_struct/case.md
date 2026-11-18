---
id: l09_cgo_nested_struct
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，嵌套 C 结构体布局）
tags: [L09, cgo, nested-struct, layout]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库三层嵌套结构：`scene_t {entity_t ents[4]; count}`，其中 `entity_t {vec2_t pos; vec2_t vel; id; label[16]}`。C 初始化/求和，Go 直接读写最内层字段，C 辅助函数平移实体。

## 期望行为

1. vec2_t/entity_t/scene_t 的 sizeof 与各字段 offsetof 的 Go/C 断言一致；
2. C 初始化后 4 个实体的 pos/vel/id/label 全部正确，C 能量和正确；
3. Go 直接改 `ents[2].vel.y`、`ents[3].pos.x/id` 后 C 求和立即反映；
4. C 平移 `ents[1]` 后 Go 读到新坐标；
5. 64 轮 GC 后嵌套字段、标签与能量和不变；最后一行 `ALL PASS l09_cgo_nested_struct`。

## 失败判读

- offset/sizeof 不一致：cgo 嵌套结构映射或对齐假设错误；
- C 未看到 Go 写入：字段偏移写错或写到了副本；
- GC 后损坏：C 保留了指向 Go 栈的指针（本用例 C 只在调用内使用）；
- 标签乱码：C 未写 NUL 或长度越界。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_nested_struct
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
