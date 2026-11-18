---
id: l12_module_init_order
dim: L12_init_link
priority: P1
sources:
  - caddy@v2.10.0: modules_test.go:22 全局模块表在包初始化后按命名空间查询
  - gitea@v1.23.5: models/renderhelper/main_test.go:14 TestMain 运行前向全局注册表注入回调
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 多个 testdata 子包以 init 汇总注册
tags: [L12, module, multi-package, package-init, init-order, var-init, cross-package, gc]
requires: [pkgbuild]
runtime: "<10s"
---

# 场景

`go.mod` 定义 module，根包 `main` 导入 `top`，`top` 再导入 `mid`、`left`、`right`，三者共同依赖 `base`，所有包把初始化标记写入共享的 `sink` 包。`mid`、`left`、`right` 是互不依赖的兄弟包，按 Go 规范它们之间的顺序未定义；用例只断言规范保证的偏序：被导入包完整初始化（先变量、后 init）早于导入者，每个包只初始化一次，`init` 晚于同包变量初始化。

# 期望行为

1. 初始化 trace 恰有 12 项：`sink.init`、`base.var`、`base.init`、`mid/left/right` 各自的 `.var/.init`、`top.var`、`top.init`、`main.init`；
2. `sink.init` 在首位，`base.var/base.init` 紧随其后；`mid/left/right` 三个块各出现一次，且每块内 `.var` 先于 `.init`；
3. `top.var/top.init` 与 `main.init` 位于最后三项，说明三个兄弟包全部完成后 `top` 才开始初始化；
4. `base.Base=10`、`mid.Mid=102`、`left.Left=11`、`right.Right=12`、`top.Top=1046`，各包的 `Ready` 标志均为 true；
5. `top.TopObserved` 为 true：`top` 的包级变量初始化能看到三个兄弟包的 init 结果；
6. main 的包级变量 `mainSnapshot` 在 `main.init` 前捕获到 11 项，证明导入包的 init 先于 main 的变量初始化；
7. 8 goroutine 只读 + 主 goroutine GC 期间值不变，GC 后 trace 不重放、不追加；
8. 最后一行 `ALL PASS l12_module_init_order`。

# 失败判读

- 构建失败找不到子包：module 路径或目录布局错误；
- trace 长度不足：某个包的 init 未执行（链接/初始化任务表丢项）；
- 兄弟块之外出现 `.init` 早于 `.var`：同包变量/init 顺序错误；
- `top.var` 早于任一兄弟包块：跨包依赖初始化顺序错误；
- `top.TopObserved` 为 false：顶层变量初始化早于依赖包 init；
- GC 后 trace 或值变化：全局初始化数据/指针扫描错误；
- 失败行 `seed=` 可用于复现并发读取的起始偏移。

# 参数

- `UAF_SEED`：决定并发只读阶段的起始偏移；所有断言与偏移无关，失败输出带种子。
- `BUILD_MODE=module`：按目录构建，以便编译多个子包。

# 运行

```bash
cd cases/L12_init_link/l12_module_init_order
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
