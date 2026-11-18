---
id: l15_ext_case_and_dir
dim: L15_build_tags
priority: P0
sources:
  - cases/L15_build_tags/l15_ext_case_and_dir/impl.go:7 唯一正常实现
  - cases/L15_build_tags/l15_ext_case_and_dir/broken.gO:1 混合大小写扩展名
  - cases/L15_build_tags/l15_ext_case_and_dir/broken.go2:1 非 .go 扩展名
  - cases/L15_build_tags/l15_ext_case_and_dir/broken.go.:1 尾随点号
  - cases/L15_build_tags/l15_ext_case_and_dir/broken.go.bak:1 多重点号备份名
  - cases/L15_build_tags/l15_ext_case_and_dir/noext:1 无扩展名
  - cases/L15_build_tags/l15_ext_case_and_dir/inner.go/bad.go:1 以 .go 结尾的目录
  - cases/L15_build_tags/l15_ext_case_and_dir/main.go:49 token 唯一性校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, file-selection, filename, extension, build-scope, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

只有扩展名精确为小写 `.go` 的普通文件参与构建。目录中放置 `.gO`、`.go2`、
尾随点号、`.go.bak`、`.gox`、无扩展名等形似文件，内容均含语法错误与重复符号；
另有一个名为 `inner.go` 的目录，里面放非法包。构建成功与 init token 唯一共同
证明这些文件/目录没有进入编译单元。

## 期望行为

1. 构建成功；
2. `impl()=="real"`、`regularConst==17`；
3. init token 恰好为 `["real"]`；
4. GC 后实现、常量与 token 集合不变；
5. 退出码 0，最后一行 `ALL PASS l15_ext_case_and_dir`。

## 失败判读

- 构建日志指向 `.gO`/`.go2`/`.bak`/`noext`：扩展名匹配过宽或大小写不敏感；
- 日志指向 `inner.go/bad.go`：构建目录枚举递归进了名字像文件的目录；
- `redeclared`：形似文件被编译；
- token 数大于 1：文件选择范围错误；
- 这类失败优先判为文件选择 bug。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_ext_case_and_dir
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
