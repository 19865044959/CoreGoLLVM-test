---
id: l12_func_init_chain
dim: L12_init_link
priority: P0
sources:
  - fiber@v2.52.6: router_test.go:25 init() 中 json.Unmarshal 填充包级路由 fixture
  - go-redis@v9.8.0: example_test.go:18 init() 中通过函数构造全局 client
  - frp@v0.67.0: pkg/config/v1/client_test.go:24 Complete() 函数填充配置默认值
tags: [L12, package-init, var-init, function-call, init-order, side-effect, gc]
requires: []
runtime: "<3s"
---

# 场景

包级变量通过函数调用链初始化：`mkInt` 记录调用顺序并计数，`b` 依赖 `a`，`c` 依赖 `b`，`d` 依赖 `c`，`e` 通过 `mkTag` 构造，`root` 通过 `registerInto` 写入全局 map 并返回指针。变量依赖链跨四个文件，引用者的文件名词法顺序不影响初始化顺序。两个 init 函数在全部变量初始化后运行。

# 期望行为

1. `a=3`、`b=7`、`c=14`、`d=15`，`e.id=42`、`e.v=15`、`e.tag="e-tag"`；
2. `root` 与 `e` 是同一指针，`registry[42] == e`，注册副作用在变量初始化阶段完成；
3. 初始化日志严格为 `a,b,c,d,e,root,init`：完全由依赖决定，不依赖文件名顺序；
4. `mkInt` 恰调用 4 次、`mkTag` 恰 1 次、`registerInto` 恰 1 次；
5. `initSeen=30`（`d+e.v`），第二个 init 看到注册表长度 1，说明 init 晚于所有包级变量；
6. 8 goroutine 只读 + GC 期间值不变，初始化函数不重跑，日志长度与内容不变；
7. GC 后 `root==e`、payload 字段与注册表指针仍有效；
8. 最后一行 `ALL PASS l12_func_init_chain`。

# 失败判读

- 日志顺序与 `a,b,c,d,e,root,init` 不符：变量依赖分析或初始化排序错误；
- 某函数计数为 0/2：初始化表达式未执行或被重复求值；
- `registry[42]` 为空：函数调用副作用在变量初始化阶段丢失；
- `initSeen` 错误：init 早于包级变量初始化；
- GC 后指针变化：全局 map/指针扫描错误；
- 失败行 `seed=` 可用于复现并发读取起始位置。

# 参数

- `UAF_SEED`：决定并发只读阶段的起始偏移；断言与偏移无关。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_func_init_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
