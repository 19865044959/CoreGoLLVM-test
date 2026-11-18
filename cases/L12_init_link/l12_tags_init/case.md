---
id: l12_tags_init
dim: L12_init_link
priority: P1
sources:
  - quic-go@v0.50.1: quic_suite_linux_test.go:1 `//go:build linux` 约束文件，:9 该文件内的 init()
  - grpc-go@v1.71.1: credentials/alts/alts_test.go:1 `//go:build linux || windows` 约束，:57 该文件内的 init()
  - tailscale@v1.80.2: util/linuxfw/iptables_runner_test.go:4 平台约束文件，:18 init()
tags: [L12, build-tags, GOOS-file-selection, custom-tag, ignore-tag, package-init, gc]
requires: [pkgbuild]
runtime: "<2s"
---

# 场景

同一 `package main` 中放置四类文件：`//go:build linux` 与 `//go:build !linux` 各提供一份 `taggedImpl` 并在 init 中写标记；`//go:build l12special`（自定义标签默认未启用）与 `//go:build ignore` 两个文件也定义 init。按当前 GOOS 只有一个平台文件参与构建，后两者必须整文件被排除。用例不比较被选中文件与根文件 init 的相对顺序（跨文件顺序未定义），只断言“哪些文件的 init 存在”。

# 期望行为

1. `initMarks` 恰有 1 项：只有一个 GOOS 选择文件贡献 init；
2. Linux 上标记为 `linux.init` 且 `taggedImpl()=="linux"`；其他系统为 `other.init`/`"other"`；
3. `special.init`、`ignore.init` 永不出现：未启用标签与 `ignore` 文件被构建系统排除；
4. 并发读取 + 显式 GC 后标记与实现选择不变，且 init 不重放；
5. 最后一行 `ALL PASS l12_tags_init`。

# 失败判读

- 标记数不为 1 或两个平台标记同时出现：构建约束解析/文件选择错误；
- `special.init` 出现：自定义标签被错误地默认为启用；
- `taggedImpl` 与 GOOS 不符：约束表达式或文件后缀选择错误；
- GC 后标记变化：初始化状态被重置。

# 参数

- `UAF_SEED`：并发 worker 起始偏移，失败信息带出种子；
- 无 TORTURE_* 参数。

# 运行

构建标签在显式列出 .go 文件时会被忽略，必须按包目录构建：

```bash
cd cases/L12_init_link/l12_tags_init
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```

手工验证另一分支可加 `-tags=l12special`（此时会多出 `special.init`，本用例预期失败）。
