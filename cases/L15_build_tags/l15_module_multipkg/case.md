---
id: l15_module_multipkg
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_module_multipkg/go.mod:1 多包 module 定义
  - cases/L15_build_tags/l15_module_multipkg/lib/lib.go:15 lib 包跨包调用 inner
  - cases/L15_build_tags/l15_module_multipkg/lib/inner/inner.go:9 传递依赖包
  - cases/L15_build_tags/l15_module_multipkg/lib/platform_linux.go:1 子包内构建标签
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, module, multi-package, cross-package-link, subpackage-build-tag, init-order, gc]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

`go.mod` 定义 module，main 包在根、导入 `lib`，`lib` 再导入 `lib/inner`（两层跨包链接）；`lib` 内部还有 `linux`/`!linux` 构建标签文件。同时验证依赖包先于 `lib` 完成初始化，以及跨包返回的指针在 GC 后有效。

## 期望行为

1. `lib.InitOK==true`：`inner` 包先初始化，`lib` 的 init 能看到依赖就绪；
2. `lib.Build(7,"multipkg")` 经 `inner.Scale`/`inner.Bias` 计算得到 `Weight==122`；
3. `lib.Platform()` 按 GOOS 返回 `linux-lib` 或 `other-lib`（子包构建标签生效）；
4. GC 后跨包 Record 字段与 main payload 不变；
5. 本地 `go build .` 通过；退出码 0，最后一行 `ALL PASS l15_module_multipkg`。

## 失败判读

- 构建失败找不到子包：module 路径/目录布局错误；
- `InitOK==false`：跨包 init 顺序错误；
- Weight 错误：跨包链接到错误符号或常量未链接；
- 子包平台值错误：子目录文件未应用构建约束。

## 参数

`BUILD_MODE=module`；`go.mod` 声明 `go 1.24`；无三方依赖。

## 运行

```bash
cd cases/L15_build_tags/l15_module_multipkg
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
