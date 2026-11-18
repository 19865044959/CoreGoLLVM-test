---
id: l12_blank_import_transitive
dim: L12_init_link
priority: P1
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 根包以整组 blank import 触发 testdata 子包的 init 注册
  - grpc-go@v1.71.1: clientconn_parsed_target_test.go:60 init() 中 resolver.Register 的注册副作用
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:21 blank import 的 imports 子包再依赖其他 testdata 子包（传递导入）
tags: [L12, module, blank-import, transitive-import, side-effect, linker-retention, package-init, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

根包只 `_ "…/plugin"`，不引用 plugin 的任何符号；plugin 又 `_ "…/inner"` 并在 init 中读取 inner init 写入共享 `sink` 的计数。另有一个 `other` 包正常 import inner，构成第二条导入路径。用例验证：blank import 的 init 副作用被链接器保留、传递依赖按 inner→plugin 顺序执行、根包变量初始化能看到副作用、第二条导入路径不会重跑 init。

# 期望行为

1. 根包包级变量 `mainSnapshot` 恰为 `[inner.init, plugin.init]`：未被直接引用的 blank-imported 包仍在最终二进制中；
2. `sink.Count("inner")==1`：传递依赖与正常导入共用同一次 init；
3. `plugin.sawInner==1`、`plugin.ready==1`：plugin init 在 inner init 之后并观察到其副作用；
4. `other.Value==6` 且 `other.Ready`：正常导入路径状态一致；
5. main.init 之后标记为 `[inner.init, plugin.init, main.init]`；
6. 并发只读 + 显式 GC 后计数与标记不变；
7. 最后一行 `ALL PASS l12_blank_import_transitive`。

# 失败判读

- `main var sees two blank-import marks` 失败：blank import 的 init 被链接器丢弃或未执行；
- `transitive blank imports run in dependency order` 失败：plugin 早于 inner init；
- `inner init ran exactly once` 失败：同一包被两条导入路径重复初始化；
- `plugin saw inner init...` 失败：传递依赖边缺失；
- GC 后计数变化：init 副作用状态未被全局表正确保留。

# 参数

- `UAF_SEED`：并发 worker 起始偏移，失败信息带出种子；
- 无 TORTURE_* 参数。

# 运行

```bash
cd cases/L12_init_link/l12_blank_import_transitive
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
