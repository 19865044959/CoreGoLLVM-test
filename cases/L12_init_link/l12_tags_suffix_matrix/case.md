---
id: l12_tags_suffix_matrix
dim: L12_init_link
priority: P1
sources:
  - quic-go@v0.50.1: quic_suite_linux_test.go:1 `//go:build linux` 与文件名 _linux 选择，:9 该文件 init()
  - gorm@v1.30.0: utils/utils_unix_test.go:1 `//go:build unix` 平台元标签
  - grpc-go@v1.71.1: channelz/service/service_sktopt_test.go:1 复合约束 `linux && (386 || amd64)`
  - fasthttp@v1.59.0: bytesconv_64_test.go:1 架构族约束 `amd64 || arm64 || ...`
tags: [L12, module, build-tags, filename-suffix, GOOS-GOARCH, unix-tag, cgo-tag, compound-constraint, package-init, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

同一 `package main` 混合文件名后缀选择（`_linux.go`、`_amd64.go`、`_linux_amd64.go`、`_windows.go`、`_386.go`）与 `//go:build` 约束（`unix`、`amd64 || arm64`、永不启用的自定义标签、`ignore`），另有 `cgo`/`!cgo` 成对文件。每个被选中文件贡献一个包级变量标记和一个 init 标记。用例按 `runtime.GOOS/GOARCH` 计算期望集合，不断言跨文件 init 顺序，并验证所有变量初始化先于任何 init。

## 期望行为

1. 实际 var/init 标记集合等于按当前平台计算的集合，每个期望 stem 恰好出现一次；
2. linux/amd64 下期望：`suffix_linux`、`suffix_amd64`、`suffix_linux_amd64`、`unix`、`pair` 各一个，加 cgo 对中恰好一个；
3. `cgo` 与 `!cgo` 文件恰有一个被选中（不假定 CGO_ENABLED 的值），var/init 成对一致；
4. 非 windows 主机不出现 `suffix_windows`；非 386 主机不出现 `suffix_386`；
5. `off.*`（`l12_suffix_off`）与 `ignore.*` 标记数量为 0；
6. 每个标记格式正确（`.var`/`.init`），var 与 init 数量相等；`phaseOK` 证明全部包级变量已初始化；
7. 8 goroutine 并发只读标记切片并穿插显式 GC 时结果不变，init 不重放；
8. 最后一行 `ALL PASS l12_tags_suffix_matrix`。

## 失败判读

- 标记集合与 GOOS/GOARCH 期望不符：文件名后缀选择或构建约束求值错误；
- 同时选中多个平台后缀文件：GOOS/GOARCH 后缀解析错误；
- cgo 对选中 0 个或 2 个：cgo 标签环境/约束解析错误；
- `off.*`/`ignore.*` 出现：未启用 tag 或 ignore 文件被错误编译；
- `phaseOK==0`：包级变量初始化与 init 的阶段顺序错误；
- GC 后标记变化：初始化期全局切片未被正确扫描。

## 参数

- `UAF_SEED`：决定并发读取起点与 GC 轮数；断言与起点无关，失败行带出种子。
- `BUILD_MODE=module`：按目录构建，确保文件名后缀与 `//go:build` 约束生效。

## 运行

```bash
cd cases/L12_init_link/l12_tags_suffix_matrix
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
