---
id: l11_inline_closure
dim: L11_compiler_opts
priority: P1
sources:
  - echo@v4.13.4: echo_test.go:313 Pre + 3 层 Use 的闭包执行顺序
  - echo@v4.13.4: group_test.go:47 全局中间件与每路由中间件的闭包切片不得互相串扰
  - golangci-lint@v2.0.2: pkg/golinters/copyloopvar/testdata/copyloopvar.go:12 循环变量冗余拷贝与闭包捕获
tags: [inline, closure, capture, escape, recursive-closure, noinline, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟中间件链与请求处理热路径中的闭包：立即调用闭包（IIFE）累加、循环内每轮显式新建变量再被闭包捕获、返回捕获自身计数器的逃逸闭包、递归匿名函数（斐波那契）、嵌套闭包共享捕获变量、`defer` 闭包修改命名返回值、闭包捕获堆对象指针，以及内联候选与 `//go:noinline` 包装的算术闭包对照。所有闭包数组与捕获对象保留到最终 GC 后再次调用。

# 期望行为

1. IIFE 累加结果等于闭式公式 `3*(n-1)*n/2+n`；
2. 循环内闭包各自捕获本轮变量，调用和等于逐轮期望值，GC 后不变；
3. 逃逸计数器闭包在 GC 后继续累加，末值等于 `base + n(n+1)/2`；
4. 内联版本与 `//go:noinline` 包装版本对同一输入给出相同算术结果（含负数右移）；
5. 递归闭包斐波那契与迭代实现逐项相等；
6. 嵌套闭包两次调用看到同一捕获变量，结果均为 `base+18`；
7. 两个闭包修改同一捕获变量后，变量与读取闭包都得到 `20`；
8. `defer` 闭包把命名返回值翻倍，结果为 `n(n-1)`；
9. 闭包捕获的 `*icBox` 跨 GC 后字段正确，继续调用返回 `16` 且对象值变 `8`；
10. 最后一行输出 `ALL PASS l11_inline_closure`。

# 失败判读

- `immediate closure sum mismatch`：内联 IIFE 丢失或重复执行；
- `loop closure sum mismatch after GC`：循环变量捕获共享同一份或闭包环境被回收；
- `escaping counter closure mismatch`：逃逸闭包的状态更新丢失；
- `noinline closure arithmetic mismatch`：内联与不内联路径结果不一致；
- `captured box corrupted by GC`：捕获的堆对象被提前回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定轮数 `32..47`、计数器基值与算术输入，失败输出带种子；
- 本用例为 P1，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_inline_closure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_inline_closure/*.go
UAF_SEED=1 /tmp/case_n
```
