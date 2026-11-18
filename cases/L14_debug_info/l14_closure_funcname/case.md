---
id: l14_closure_funcname
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_closure_funcname/main.go:56 makeAdder 闭包工厂
  - cases/L14_debug_info/l14_closure_funcname/main.go:61 panic 闭包工厂
  - cases/L14_debug_info/l14_closure_funcname/main.go:69 捕获 panic 并抓栈
  - cases/L14_debug_info/l14_closure_funcname/main.go:81 reflect funcval 反查符号
  - docs/DIMENSIONS.md:22 L14 调试信息（闭包符号）
tags: [L14, closure-symbol, funcval, reflect, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

同一 noinline 工厂返回带不同捕获值的闭包，通过 `reflect.ValueOf(f).Pointer()` + `runtime.FuncForPC` 反查闭包真实符号；panic 闭包走间接调用后在 defer 中 recover 抓栈。覆盖闭包 funcval 到符号表的映射。

## 期望行为

1. 两个 `makeAdder` 闭包分别捕获 3/9，调用结果为 7/13；
2. 两个闭包的 funcval 都解析为 `main.makeAdder.func1`（同工厂共享代码符号）；
3. panic 闭包解析为 `main.makePanicClosure.func1`；
4. traceback 包含 `main.makePanicClosure.func1(`、`main.callPanicClosure(`、`goroutine` 头与 `main.go:<行号>`；
5. recover 到的 payload 与 panic 时同一对象，GC 后 id/next 仍有效；
6. 退出码 0，最后一行 `ALL PASS l14_closure_funcname`。

## 失败判读

- funcval 名称不是 `.func1`：闭包符号命名错误或符号表缺失；
- 栈中闭包帧符号缺失：traceback 未使用闭包真实符号；
- 捕获值错误：闭包环境变量栈映射漏标。

## 参数

无随机路径。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_closure_funcname/*.go
GODEBUG=clobberfree=1 /tmp/case
```
