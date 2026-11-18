---
id: l12_subpkg_init_module
dim: L12_init_link
priority: P1
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 以 blank import 引入 23 个 testdata 子包，子包 init 注册生成描述符
  - gitea@v1.23.5: models/renderhelper/main_test.go:14 TestMain 在测试前向子包 markup 注入初始化回调
  - grpc-go@v1.71.1: clientconn_parsed_target_test.go:60 子包 init 向全局 resolver 注册表登记
tags: [L12, module, subpackage-init, dependency-order, runs-once, second-importer, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

module 内 `inner` 是最底层子包，`lib` 导入 `inner`，`other` 是 `inner` 的第二个导入者。三个包分别在包级变量初始化和 `init` 中写入共享 `sink` 标记；`lib.init` 末尾抓取快照，根包用包级变量 `mainSnapshot = lib.Snapshot()` 验证根变量初始化发生在子包 init 之后。`other` 与 `lib` 是兄弟包，相对顺序未定义，因此不产生标记、不做先后断言。

# 期望行为

1. `inner.InitCount()==1`、`other.SeenInner()==1`：第二个导入者不会重跑 `inner` 的 init；
2. 全局标记严格为 `inner.var, inner.init, lib.var, lib.init, main.init`：线性依赖 inner→lib 的顺序由规范保证；
3. `inner.Value==7`、`lib.Value==71`、`other.Value==8`，`lib.InnerReady()`、`other.Ready` 为真：导入者的变量/ init 能看到被导入包已完成初始化；
4. `mainSnapshot` 等于前 4 个标记：根包包级变量晚于子包 init；
5. 并发只读 + 显式 `runtime.GC()` 后标记、计数与值不变。

# 失败判读

- `inner init runs exactly once` 失败：重复初始化或计数器被重置；
- `subpackage init order is dependency order` 失败：init/变量初始化顺序违反依赖偏序；
- `main var initializer sees four subpackage marks` 失败：导入者变量早于子包 init（链接/初始化调用序错误）；
- GC 后状态变化：初始化期构造的全局状态未被正确扫描。

# 参数

- `UAF_SEED`：选择并发 worker 的起始偏移，失败信息带出种子；
- 无 TORTURE_* 参数。

# 运行

```bash
cd cases/L12_init_link/l12_subpkg_init_module
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
