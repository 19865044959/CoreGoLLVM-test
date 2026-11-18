---
id: l14_generic_func_traceback
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_generic_func_traceback/main.go:61 - 泛型叶子函数 gpLeaf[T] panic
  - cases/L14_debug_info/l14_generic_func_traceback/main.go:67 - 泛型链 gpOuter[T] 的 recover 抓帧
  - cases/L14_debug_info/l14_generic_func_traceback/main.go:95 - 泛型方法值 / 方法表达式符号
  - cases/L14_debug_info/l14_generic_func_traceback/main.go:98 - 泛型方法内的闭包
  - docs/DIMENSIONS.md:22 - L14 调试信息（栈展开/行号）
tags: [L14, generics, instantiation, traceback, CallersFrames, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

泛型函数 `gpOuter[T] -> gpMid[T] -> gpLeaf[T]` 分别在 T=int 和 T=string 上实例化后 panic，在 defer 中 recover 并抓 `debug.Stack()` 与 `runtime.CallersFrames`。同时检查泛型方法值（`(*gpBox[T]).Boom`）、泛型方法表达式和泛型方法内闭包的运行期符号。对应泛型库（pgx、ent、quic-go 等）在实例化路径崩溃时依赖真实 `[...]` 符号定位问题。

## 期望行为

1. `runtime.FuncForPC` 反查 `gpLeaf[int]` / `gpLeaf[string]` 得到形如 `main.gpLeaf[...]` 的带方括号真实符号；
2. int 与 string 两个 `funcval` 指针不同，证明实例化使用不同代码入口；
3. 泛型方法值为 `main.(*gpBox[...]).Boom-fm`，方法表达式为 `main.(*gpBox[...]).Boom`，泛型方法内闭包为 `main.(*gpBox[...]).Make.func1`；
4. 两种实例化的 panic 栈均包含 `main.gpLeaf[...]`、`main.gpMid[...]`、`main.gpOuter[...]` 与 `main.go:<行号>`，recover 到的 payload 与传入对象同一；
5. 帧表中每个泛型帧的 File 以 `main.go` 结尾、Line>0，且 `runtime.FuncForPC(Entry).Name()` 与帧名一致；
6. 显式 GC 后泛型容器与 payload 链仍有效，最后一行 `ALL PASS l14_generic_func_traceback`。

## 失败判读

- 符号不含 `[` / `]`：泛型实例化符号或运行时名字映射错误；
- int 与 string 的 funcval 指针相同：实例化入口复用错误；
- traceback 缺少 `gpLeaf[`/`gpMid[` 或行号为 0：内联信息/行号表缺失；
- Entry 反查与帧名不一致：调试信息中的函数归属错误。

## 参数

无随机路径。所有泛型函数均 `//go:noinline`，因此帧必须出现在 traceback 中；断言只依赖符号前缀/后缀与相对行号，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_generic_func_traceback/*.go
GODEBUG=clobberfree=1 /tmp/case
```
