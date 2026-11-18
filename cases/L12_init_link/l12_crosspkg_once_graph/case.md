---
id: l12_crosspkg_once_graph
dim: L12_init_link
priority: P2
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/annotations/annotations.pb.go:126 rawDescOnce sync.Once，:131 Once.Do 懒解析描述符
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/mocks_test.go:677 file_message_proto_rawDescOnce sync.Once
  - tailscale@v1.80.2: util/codegen/codegen_test.go:454 var namedTestTypes = sync.OnceValues(...) 跨阶段依赖的懒初始化
  - gitea@v1.23.5: modules/globallock/globallock_test.go:34 包级 initOnce sync.Once
tags: [L12, module, sync.Once, cross-package, dependency-DAG, lazy-init, concurrency, gc, TORTURE_ROUNDS]
requires: [pkgbuild]
runtime: "<10s（默认 32 goroutine × 64 迭代；TORTURE_ROUNDS 放大到 128 × 2048）"
---

# 场景

module 中 `base` 是 `sync.Once` 依赖图的叶子，`left`/`right` 两个兄弟包各自的 Once body 先调用 `base.Ensure`，`root` 的 Once body 再调用两个分支。imports 决定 init 偏序 `base -> left/right -> root -> main`，left 与 right 的相对顺序未定义。各包 init 记录此刻的构建次数（必须为 0）；主程序用大量 goroutine 并发首次调用 `root.Ensure()`，主 goroutine 同时分配垃圾并显式 GC。

## 期望行为

1. init 标记恰 5 个（base/left/right/root/main 各一），且满足 `base < left`、`base < right`、`left/right < root`、`root < main`；left 与 right 的相对顺序不断言；
2. 所有 `InitBuilds==0`、所有 `Builds()==0`：init 阶段没有触发懒构建；
3. 并发首次调用后每个 Once body 恰执行一次，`base.Builds()/left.Builds()/right.Builds()/root.Builds()` 均为 1；
4. 依赖指针身份：`root.Left==left.Ensure()`、`root.Right==right.Ensure()`、两个分支的 `Base==base.Ensure()`；ID/Label 分别为 100/21/22/11；
5. 构建标记恰 4 个，且 `base.build` 早于两个分支，两个分支早于 `root.build`（left/right 相对顺序不断言）；
6. 并发 worker 全部看到同一张指针图，主 goroutine GC 期间无错误；
7. 第二次 `Ensure` 返回同一指针且构建计数不增；GC 后整张图与标记不变；
8. `TORTURE_ROUNDS`（默认 1，上限 8）放大 worker 数与迭代数；
9. 最后一行 `ALL PASS l12_crosspkg_once_graph`。

## 失败判读

- `InitBuilds != 0`：init 阶段错误地执行了懒构建；
- 构建数 >1：Once 内存模型或跨包可见性错误；
- 依赖指针身份错误：Once body 未等待依赖完成或全局指针被破坏；
- 构建偏序失败：跨包依赖未建立 happens-before；
- GC 后图失效：全局 Once 结果未被正确扫描；
- 超时：Once 相互等待或重入死锁。

## 参数

- `TORTURE_ROUNDS=1..8`：worker 数 = 32×rounds（上限 128），迭代数 = 64×rounds（上限 2048）；
- `UAF_SEED`：worker 循环起始偏移，失败信息带出种子。

## 运行

```bash
cd cases/L12_init_link/l12_crosspkg_once_graph
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
