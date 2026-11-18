---
id: l15_tag_cycle_break
dim: L15_build_tags
priority: P2
sources:
  - cases/L15_build_tags/l15_tag_cycle_break/a/link_linux.go:1 linux 下 a 导入 b
  - cases/L15_build_tags/l15_tag_cycle_break/a/link_other.go:1 非 linux 下 a 不导入 b
  - cases/L15_build_tags/l15_tag_cycle_break/b/link_other.go:1 非 linux 下 b 导入 a
  - cases/L15_build_tags/l15_tag_cycle_break/trace/trace.go:8 包 init 标记
  - cases/L15_build_tags/l15_tag_cycle_break/main.go:91 期望 init 顺序
  - cases/L15_build_tags/l15_tag_cycle_break/main.go:121 TORTURE_ROUNDS 遍历依赖边
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, multi-package, import-cycle, GOOS, init-order, TORTURE_ROUNDS, gc]
requires: [pkgbuild]
runtime: "<10s (TORTURE_ROUNDS 放大)"
---

# 场景

构建标签可以改变 import 图，从而让一个在字面上互相引用的包集合在任一平台都
不形成环。linux 下包 a 的依赖边指向 b（b 的 linux 文件不导入 a）；非 linux 下
包 b 的依赖边指向 a（a 的非 linux 文件不导入 b）。两个方向都编译通过，且
import 方向、包 init 顺序、跨包取值随 GOOS 切换。

## 期望行为

1. linux：`a.Partner()=="a->b"`、`b.Helper()=="b-local:local-b"`；
2. 非 linux：`a.Partner()=="a-standalone"`、`b.Helper()=="b->a"`；
3. init 序列 linux 为 `trace,b,a,main`，非 linux 为 `trace,a,b,main`；
4. 每个包 init 标记恰好出现一次；
5. TORTURE_ROUNDS 轮拼接依赖边结果的长度恒为平台固定值，校验和等于 `R*len`；
6. 最终 GC 后依赖边结果、init 序列、payload 不变；
7. 退出码 0，最后一行 `ALL PASS l15_tag_cycle_break`。

## 失败判读

- `import cycle not allowed`：两个互斥依赖文件同时参与编译，tag 排他失败；
- 某平台可执行文件缺失：非 linux 分支的互斥文件约束写错；
- init 顺序相反或重复：import 图方向错误或 init 排序错误；
- 跨包值来自另一分支：GOOS 文件选择错误；
- 校验和不符：循环中对象/字符串被破坏。

## 参数

- `TORTURE_ROUNDS`：默认 5000，可放大；
- `UAF_SEED`：写入失败信息，便于固定复现；
- `BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_tag_cycle_break
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
GOOS=windows GOARCH=amd64 GOTOOLCHAIN=go1.24.1 go build -o /tmp/case.exe .
TORTURE_ROUNDS=100000 /tmp/case
```
