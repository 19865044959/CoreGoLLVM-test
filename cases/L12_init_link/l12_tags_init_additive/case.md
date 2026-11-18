---
id: l12_tags_init_additive
dim: L12_init_link
priority: P1
sources:
  - sonic@v1.13.2: encode_test.go:1 复合构建约束 `(amd64 && go1.17 && !go1.25) || (arm64 && go1.20 && !go1.25)`，:238 该文件内 init
  - nats-server: server/msgtrace_test.go:14 `//go:build !skip_msgtrace_tests`，:36 该文件内 init
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/nameclash/test_name_clash_hybrid/test_name_clash_hybrid.pb.go:27 `//go:build !protoopaque`，:3846 生成代码 init
tags: [L12, module, build-tags, compound-constraint, additive-files, package-init, GOOS-GOARCH, gc]
requires: [pkgbuild]
runtime: "<3s"
---

# 场景

同一 `package main` 中放置多个无文件名后缀、仅靠 `//go:build` 约束选择的文件：`linux`、`linux && amd64`、`!windows`、`windows` 各在包级变量初始化与 init 中写标记；另有 `l12_additive_off`（自定义标签默认未启用）与 `ignore` 两个必须整体排除的文件。与“互斥选择”不同，本用例验证同时为真的约束文件必须**叠加**贡献，且排除文件的标记绝不出现。跨文件 init 顺序未定义，不做断言。

## 期望行为

1. 实际标记集合等于按 `runtime.GOOS/runtime.GOARCH` 计算的期望集合：
   - linux 贡献 `linux.var/init`；
   - linux&&amd64 贡献 `linux_amd64.var/init`；
   - !windows 贡献 `notwindows.var/init`；
   - windows 贡献 `windows.var/init`；
2. 每个应出现的标记恰出现一次，长度与期望一致；
3. `off.*` 与 `ignore.*` 标记数量为 0；
4. 所有 `.var` 标记都早于所有 `.init` 标记（包级变量初始化整体先于 init）；
5. 并发只读标记切片并穿插显式 GC 时结果不变，init 不重放；
6. 最后一行 `ALL PASS l12_tags_init_additive`。

## 失败判读

- 标记集合与 GOOS/GOARCH 期望不符：构建约束表达式求值或文件选择错误；
- 同时为真的文件缺标记：约束被错误当成互斥选择；
- `off.*`/`ignore.*` 出现：未启用 tag/ignore 文件被错误编译；
- `.var` 晚于 `.init`：同包初始化阶段顺序错误；
- GC 后标记变化：初始化期全局切片未被正确扫描。

## 参数

- `UAF_SEED`：决定并发读取的标记轮换起点；断言与起点无关。
- `BUILD_MODE=module`：按目录构建，确保 `//go:build` 约束生效。

## 运行

```bash
cd cases/L12_init_link/l12_tags_init_additive
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
