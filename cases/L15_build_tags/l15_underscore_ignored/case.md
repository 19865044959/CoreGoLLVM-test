---
id: l15_underscore_ignored
dim: L15_build_tags
priority: P0
sources:
  - cases/L15_build_tags/l15_underscore_ignored/_broken.go:1 下划线文件含非法语法
  - cases/L15_build_tags/l15_underscore_ignored/.hidden_broken.go:1 点号文件含非法语法
  - cases/L15_build_tags/l15_underscore_ignored/_dup.go:1 下划线文件重复定义 impl
  - cases/L15_build_tags/l15_underscore_ignored/.hidden_token.go:1 点号文件只登记 token
  - cases/L15_build_tags/l15_underscore_ignored/main_test.go:1 测试文件重复定义
  - cases/L15_build_tags/l15_underscore_ignored/main.go:59 忽略文件哨兵断言
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-constraint, underscore-ignore, dotfile-ignore, test-file-exclusion, file-selection, gc]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

构建系统按约定忽略 `_`/`.` 开头的文件和 `_test.go`。目录中故意放置：非法语法
的 `_broken.go`/`.hidden_broken.go`、重复定义 `impl` 的 `_dup.go`/`.hidden_dup.go`/
`main_test.go`、以及能编译但只登记 token 的 `_token.go`/`.hidden_token.go`。
构建成功说明前两类被排除；token 精确等于 `["real"]` 说明后一类也被排除。

## 期望行为

1. `go build .` 成功，未因非法语法或重复定义失败；
2. impl()=="real"、regularConst==7、regularMarker()=="regular"；
3. init token 集合恰为 `["real"]`，无 underscore/hidden/test token；
4. GC 后值与 token 集合不变；
5. 退出码 0，最后一行 `ALL PASS l15_underscore_ignored`。

## 失败判读

- 构建报语法错误：`_`/`.` 前缀文件被错误纳入；
- 构建报 `impl redeclared`：下划线文件或 `_test.go` 被错误纳入；
- token 数量 >1：某类忽略规则漏掉（含能编译的文件）；
- 值为 dup/test/hidden：选择了错误的实现文件。

## 参数

`BUILD_MODE=module`；无自定义 tag；不读取 UAF_SEED。

辅助核对：

```bash
GOTOOLCHAIN=go1.24.1 go list -f '{{.GoFiles}}' .
```

## 运行

```bash
cd cases/L15_build_tags/l15_underscore_ignored
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
