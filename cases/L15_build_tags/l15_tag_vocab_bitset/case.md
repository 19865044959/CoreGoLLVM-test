---
id: l15_tag_vocab_bitset
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_tag_vocab_bitset/t_unix_tag.go:1 unix 元标签
  - cases/L15_build_tags/l15_tag_vocab_bitset/t_compiler_gc_tag.go:1 gc 编译器标签
  - cases/L15_build_tags/l15_tag_vocab_bitset/t_release_go124_tag.go:1 go1.24 release 标签
  - cases/L15_build_tags/l15_tag_vocab_bitset/t_exact_combo_tag.go:1 七标签合取
  - cases/L15_build_tags/l15_tag_vocab_bitset/t_lookalike_tag.go:1 形似标签不命中
  - cases/L15_build_tags/l15_tag_vocab_bitset/main.go:64 期望 token 集合推导
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, GOOS, GOARCH, unix-meta-tag, compiler-tag, release-tag, tag-vocabulary, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

模拟三方库在构建标签里混用真实标签与自定义标签的写法：`unix` 元标签、`gc`/`gccgo`
编译器标签、`go1.N` release 标签，以及与真实标签形似的自定义标签 `linuxx`、`go1`、
`gc1`、`Linux`。每个命中文件在 init 中登记一个 token，main 用
`runtime.GOOS/GOARCH/Compiler` 推导期望集合后逐项比对，任何前缀/大小写误匹配都会
表现为多余或缺失 token。

## 期望行为

1. linux 构建且 amd64 时 token 集合包含 os-linux、meta-unix、arch-amd64、compiler-gc、release-go1.24、exact-combo、lookalike-absent；
2. 形似标签文件（linuxx/go1/gc1/Linux 等）不参与编译，`lookalike` token 计数为 0，其严格补集文件必须命中；
3. token 排序后与期望集合完全一致，无重复；
4. GC 后 token 集合与 payload 不变；
5. 退出码 0，最后一行 `ALL PASS l15_tag_vocab_bitset`。

## 失败判读

- 多余 `lookalike` token：标签前缀或大小写被宽松匹配；
- 缺少 `meta-unix`：`unix` 元标签未展开成 GOOS 集合；
- `compiler-gc`/`compiler-gccgo` 同时出现或都缺失：编译器标签处理错误；
- `release-go1.24` 缺失或 `release-go1.25` 出现：release 标签阈值错误；
- 构建报重复符号：互补表达式不是严格取反。

## 参数

- 目标工具链固定 `go1.24.1`（`go1.24` 成立、`go1.25` 不成立）；`BUILD_MODE=module`；
- 无自定义 tag；手工传 `-tags=linuxx` 时失败属于预期（形似标签被人为设置）。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_vocab_bitset
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
