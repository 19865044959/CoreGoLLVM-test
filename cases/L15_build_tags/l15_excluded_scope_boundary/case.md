---
id: l15_excluded_scope_boundary
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_excluded_scope_boundary/bomb_test.go:1 语法错误的 _test.go
  - cases/L15_build_tags/l15_excluded_scope_boundary/bomb_embed_ignore.go:9 被 ignore 排除的非法 embed
  - cases/L15_build_tags/l15_excluded_scope_boundary/bomb_embed_tag.go:9 被 tag 排除的包名/embed 炸弹
  - cases/L15_build_tags/l15_excluded_scope_boundary/base.go:7 唯一正常实现
  - cases/L15_build_tags/l15_excluded_scope_boundary/main.go:49 init 计数与状态校验
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, file-selection, test-file, embed, parse-order, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

普通 `go build` 的构建范围排除发生在解析包体之前。目录中放置：一个有语法错误、
重复符号和 init 的 `_test.go`；一个有非法 `//go:embed` 模式、语法错误的
`//go:build ignore` 文件；一个不同 package 名、embed 模式含 `..` 的 tag 排除文件。
只要构建成功，就说明这些文件没有进入包体解析、包名检查与 embed 扫描。

## 期望行为

1. 构建成功，三个炸弹文件均被排除；
2. `baseImpl()=="base"`、`baseConst==7`；
3. `baseInits==1`，只有 base.go 的 init 执行；
4. 两次 GC 后上述值不变；
5. 退出码 0，最后一行 `ALL PASS l15_excluded_scope_boundary`。

## 失败判读

- 构建日志含 `expected ')'` 或指向 `bomb_test.go`：构建范围错误地解析了测试文件；
- 日志含 `no matching files found` / `invalid pattern`：被排除文件的 embed 被处理；
- 日志含 `found packages main and otherpkg`：包名检查未先应用构建约束；
- `baseInits!=1`：运行期仍链接了被排除文件的 init；
- 这类失败优先判为构建标签/文件选择语义 bug。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_excluded_scope_boundary
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.exe .
```
