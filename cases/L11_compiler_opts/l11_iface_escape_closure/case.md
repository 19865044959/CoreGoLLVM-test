---
id: l11_iface_escape_closure
dim: L11_compiler_opts
priority: P2
sources:
  - echo@v4.13.4: echo_test.go:313 TestEchoMiddleware Pre/Use 闭包链依次包装 handler
  - echo@v4.13.4: group_test.go:47 TestGroupRouteMiddleware 中间件切片不得被复用
  - gorm@v1.30.0: tests/hooks_test.go:227 BeforeCreate 钩子在回调中派生并改写副本
tags: [escape-analysis, closure, interface, capture, loop-variable, any, map, typed-nil, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 3 轮；TORTURE_ROUNDS=50 时 150 轮）"
---

# 场景

模拟 Web 中间件闭包、路由分组与 ORM 回调中“接口值与堆对象只经闭包可见”的逃逸路径：每轮构造 10..24 个节点，为每个节点绑定一个接口实现（值实现或指针实现），把捕获两者的闭包存入 `[]func`、`[]any`、struct 的 `any` 字段与 `map[int]func`；显式 GC 后逐个调用并核对动态类型、值、标签与后续修改。另测闭包捕获 typed nil、计数器自增以及只由 map 保活的闭包。

# 期望行为

1. 闭包返回值等于 `node.id+impl.Value()`，标签等于 `node.tag+"/"+impl.Label()`，且各元素绑定自己迭代的接口实现；
2. 存入 `any` 的闭包可断言回 `func() (int64,string)` 并返回同样结果；
3. map 中每个闭包在 GC 后返回创建时捕获的 `id*2+len(tag)`；
4. 计数器闭包在 GC 后调用一次后恰增加 5；
5. 闭包捕获的 typed nil 接口在 GC 后仍 `!= nil`，断言成功且得到 nil 指针；
6. 修改捕获节点的 `id` 后闭包立即看到新值；
7. struct `any` 字段中的闭包与捕获节点在最终 GC 后完整；
8. 所有轮次的闭包、节点和 map 在最终 GC 后仍逐项匹配期望；
9. 最后一行输出 `ALL PASS l11_iface_escape_closure`。

# 失败判读

- `closure captured interface after GC` / `boxed closure after GC`：闭包环境或接口装箱未被正确保活/复制；
- `map closure value after GC`：map 中捕获的接口值被回收或串扰；
- `typed nil captured by closure`：闭包中 typed nil 的接口 nil 语义被改写；
- `closure sees mutation after GC`：捕获的是接口副本而非同一对象；
- `counter closure increment`：闭包捕获的变量被错误复制；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮节点数 10..24 与对象 id，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（3 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_iface_escape_closure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_iface_escape_closure/*.go
UAF_SEED=1 /tmp/case_n
```
