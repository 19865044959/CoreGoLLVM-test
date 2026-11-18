---
id: l15_file_init_lexical
dim: L15_build_tags
priority: P1
sources:
  - cases/L15_build_tags/l15_file_init_lexical/a_init.go:3 同一文件内多个 init 按源码顺序
  - cases/L15_build_tags/l15_file_init_lexical/b_linux_init.go:1 tag 文件按文件名字典序插入
  - cases/L15_build_tags/l15_file_init_lexical/m_init.go:3 中间文件 init
  - cases/L15_build_tags/l15_file_init_lexical/z_init.go:3 末尾文件 init
  - cases/L15_build_tags/l15_file_init_lexical/main.go:56 期望序列
  - docs/DIMENSIONS.md:23 L15 构建标签与多平台
tags: [L15, build-tag, init-order, file-name-order, GOOS, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

同一包内多个文件各有 `init` 时，go 命令按文件名字典序把文件交给编译器，
`init` 按文件顺序、文件内源码顺序执行。`b_linux_init.go` / `b_other_init.go`
由 GOOS 标签二选一，分别插入字典序中 `a_init.go` 与 `m_init.go` 之间的槽位。
这验证包内跨文件 init 顺序，而不只是"平台 init 是否执行"。

## 期望行为

1. linux 下 init 序列恰为 `a1,a2,b-linux,m,z`；
2. 非 linux 下恰为 `a1,a2,b-other,m,z`；
3. 同一文件 a_init.go 的两个 init 按源码顺序执行（a1 在 a2 前）；
4. tag 文件创建的 `payload`（v=21，name="tagged-init"）在 GC 后仍有效；
5. init 序列在 GC 前后不变；
6. 退出码 0，最后一行 `ALL PASS l15_file_init_lexical`。

## 失败判读

- 平台标记缺失或两个平台标记同时出现：GOOS 文件选择错误；
- 顺序错乱但集合正确：文件字典序未生效或编译器打乱文件顺序；
- a2 在 a1 前：同文件 init 源码顺序错误；
- payload 为空：tag 文件的 init 未执行；
- 运行期空指针/数据破坏：init 期间对象未正确初始化。

## 参数

- 无额外参数；`BUILD_MODE=module`。

## 运行

```bash
cd cases/L15_build_tags/l15_file_init_lexical
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && /tmp/case
```
