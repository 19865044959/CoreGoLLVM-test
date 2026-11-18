---
id: l15_goos_init
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_goos_init/init_linux.go:1 linux 专属 init
  - cases/L15_build_tags/l15_goos_init/init_other.go:1 其他 GOOS 的 init
  - cases/L15_build_tags/l15_goos_init/init_common.go:3 通用 init
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, GOOS, init-order, build-tag, file-selection]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

linux/windows/darwin/other 四个平台文件各带一个 `init()`，向标记切片追加自己的名字并写入 `platformInit`；通用 `init_common.go` 另外设置 payload 指针。验证只有当前 GOOS 对应的 init 执行，且平台 init 与通用 init 都在 `main` 之前完成。

## 期望行为

1. `commonInit==true` 且 `initPayload` 为 init 中创建的节点（v=11）；
2. `platformInit` 与当前 `runtime.GOOS` 映射一致（linux/windows/darwin/other）；
3. `platformInits` 恰好 1 个元素且等于当前平台名；
4. GC 后 init 创建的 payload 仍然有效；
5. 退出码 0，最后一行 `ALL PASS l15_goos_init`。

## 失败判读

- 平台 init 未执行或执行了两个：GOOS 约束解析/文件选择错误；
- init 顺序导致 main 看到零值：init 调度或包初始化顺序错误；
- GC 后 payload 损坏：包级变量写屏障/栈映射问题。

## 参数

`BUILD_MODE=module`；不依赖自定义 tag。

## 运行

```bash
cd cases/L15_build_tags/l15_goos_init
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
