---
id: l15_ignored_extensions
dim: L15_build_tags
priority: P0
sources:
  - cases/L15_build_tags/l15_ignored_extensions/broken.go.txt:1 非 .go 的形似文件
  - cases/L15_build_tags/l15_ignored_extensions/broken.GO:1 大写扩展名
  - cases/L15_build_tags/l15_ignored_extensions/broken.go~:1 编辑器备份文件
  - cases/L15_build_tags/l15_ignored_extensions/sub/ignored.go:1 未导入子目录的非法文件
  - cases/L15_build_tags/l15_ignored_extensions/main.go:54 唯一 init token 校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, file-selection, filename, build-scope, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

只有扩展名精确为小写 `.go` 的文件参与包构建。目录中放置 `.go.txt`、`.GO`、
`.go~`、`.go.orig` 等形似文件（内容含语法错误与重复符号），以及一个未被
导入的子目录中的非法包。构建成功与 init token 唯一共同证明这些文件/目录
没有进入编译单元。

## 期望行为

1. 构建成功；
2. `impl()=="real"`、`regularConst==17`；
3. init token 恰好为 `["real"]`；
4. GC 后实现、常量与 token 集合不变；
5. 退出码 0，最后一行 `ALL PASS l15_ignored_extensions`。

## 失败判读

- `BUILD-FAIL` 且日志含 `go.txt`/`GO`/`go.orig`：扩展名过滤错误；
- `redeclared`：形似文件被编译；
- token 数大于 1：构建目录枚举过宽，或子目录被递归编译；
- 这类失败优先判为文件选择 bug。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_ignored_extensions
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
