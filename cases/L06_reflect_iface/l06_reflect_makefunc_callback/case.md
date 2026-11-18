---
id: l06_reflect_makefunc_callback
dim: L06_reflect_iface
priority: P2
sources:
  - caddy@v2.10.0: replacer_test.go:240 ReplacerFunc provider 回调链逐个查询键值
  - caddy@v2.10.0: replacer_test.go:347 reflect.ValueOf(fn).Pointer() 校验函数身份
  - echo@v4.13.4: echo_test.go:1220 反射批量注册方法回调
tags: [L06, reflect, MakeFunc, callback, closure, variadic, sort, panic, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

用 `reflect.MakeFunc` 生成谓词、多返回值（值+error）、指针返回、`sort.SliceStable` less、
`strings.Map` rune 变换、变参与有状态累加等回调，作为普通 func 传入高阶函数；覆盖直接调用、
`Call`、`CallSlice`、nil 参数、回调内部 panic 传播与 MakeFunc 返回数量错误。回调及其返回值存入
全局槽，GC 后继续调用并校验闭包捕获的切片、指针与状态。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_makefunc_callback`；
2. MakeFunc 生成的函数类型正确，作为谓词/排序/rune 回调与直接手写函数行为一致；
3. 多返回值回调正常返回值和 error；指针返回回调的 Tag 与参数共享；
4. 变参回调在直接调用、Call、CallSlice 三条路径结果一致；
5. 有状态闭包跨多次调用累加正确；返回函数回调可链式调用；
6. 回调内部 panic 原样传播，MakeFunc 返回数量错误必须 panic；
7. 回调、返回值与闭包捕获对象在 GC 后可用，默认 16 轮，支持 `TORTURE_ROUNDS` 放大。

## 失败判读

- `predicate ...` / `MakeFunc less callback sorts`：MakeFunc 参数/返回值打包错误；
- `multi-return callback ...`：多返回值或 error 零值构造错误；
- `callback panic propagates` / `MakeFunc wrong return count did not panic`：panic 路径缺失；
- `... after GC`：MakeFunc 闭包环境或返回值指针未被扫描；
- 崩溃：变参回调 wrapper 的栈图/寄存器映射错误。

## 参数

- `UAF_SEED`：每轮元素数量与基础值来源；失败行带出种子；
- `TORTURE_ROUNDS`：轮数（默认 16）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_makefunc_callback/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=200 UAF_SEED=7 /tmp/case
```
