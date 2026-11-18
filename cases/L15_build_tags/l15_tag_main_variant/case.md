---
id: l15_tag_main_variant
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_main_variant/main_linux.go:1 linux 入口 main
  - cases/L15_build_tags/l15_tag_main_variant/main_windows.go:1 windows 入口 main
  - cases/L15_build_tags/l15_tag_main_variant/main_other.go:1 其余平台入口 main
  - cases/L15_build_tags/l15_tag_main_variant/main.go:49 入口参数与 GOOS 校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOOS, main-entry, mutual-exclusion, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

`main` 函数本身也可以由构建标签选择。三个互斥文件分别定义 `func main()`，
链接期必须恰好选中一个；共享文件不含 main，只放校验逻辑。若标签选择错误，
入口传入的平台名与偏置会和 `runtime.GOOS` 不一致，或链接期直接报重复 main。

## 期望行为

1. linux 下入口为 `runMain("linux", 7)`；
2. windows 下入口为 `runMain("windows", 5)`；
3. 其余 GOOS 入口为 `runMain("other", 3)`；
4. 入口平台名与根据 `runtime.GOOS` 推导的期望一致；
5. GC 后入口 payload 与平台判定不变；
6. 退出码 0，最后一行 `ALL PASS l15_tag_main_variant`。

## 失败判读

- `main redeclared` / `multiple definition of main`：互斥约束写错，多个入口被链接；
- `function main is undeclared`：所有入口文件都被排除；
- 平台名或偏置不符：选中了另一平台的 main，GOOS 标签错误；
- 这类失败定位到入口文件选择，而非业务逻辑。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_main_variant
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.exe .
```
