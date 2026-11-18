---
id: l14_multi_init_closure_symbols
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_multi_init_closure_symbols/main.go:35 - 包级 var 初始化器闭包 idVarClosure
  - cases/L14_debug_info/l14_multi_init_closure_symbols/main.go:41 - 第一个显式 func init() 注册两条闭包
  - cases/L14_debug_info/l14_multi_init_closure_symbols/main.go:46 - 第二个显式 func init()
  - cases/L14_debug_info/l14_multi_init_closure_symbols/main.go:51 - init 内调用的闭包工厂 idMakeClosure
  - cases/L14_debug_info/l14_multi_init_closure_symbols/main.go:144 - init.0 / init.1 符号断言
  - docs/DIMENSIONS.md:22 - L14 调试信息（init/闭包符号）
tags: [L14, init, closure, symbol, multiple-init, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

同一文件同时存在：包级 `var` 初始化器中的函数字面量、两个显式 `func init()` 内的闭包、以及第一个 init 调用的包级闭包工厂。Go 1.24.1 的运行期符号分别为 `main.init.funcN`、`main.init.0.funcN`、`main.init.1.funcN`、`main.init.0.idMakeClosure.funcN`。四类闭包 panic 后 recover 抓栈，验证 init 期闭包的命名空间、互不覆盖与真实行号。

## 期望行为

1. `idVarClosure` 的符号以 `main.init.` 开头，且不带显式 init 序号（不是 `main.init.0.`/`main.init.1.`）；
2. 第一个显式 init 注册的字面量符号以 `main.init.0.` 开头，其工厂闭包符号含 `idMakeClosure.func`；第二个显式 init 的字面量符号以 `main.init.1.` 开头；
3. 四个闭包符号两两不同，且都以 `main.` 开头；
4. 四类闭包 panic 的 traceback 都包含各自完整符号与 `main.go:<行号>`，recover 到的 payload 指针/ID 与传入一致；
5. 每个闭包帧 File 以 `main.go` 结尾、Line>0，`FuncForPC(Entry).Name()` 与帧名一致；traceback 带 goroutine 头；
6. 显式 init 与 var 初始化器的闭包符号不会互相覆盖；
7. 显式 GC 后 init 期闭包与捕获的 payload 链仍有效，最后一行 `ALL PASS l14_multi_init_closure_symbols`。

## 失败判读

- 包级 var 闭包落入 `main.init.0./1.`：init 函数编号错误；
- 两个显式 init 闭包同符号：多个 init 的闭包命名空间未区分；
- 工厂闭包缺少工厂名：嵌套闭包归属错误；
- 行号为 0 或 File 非 main.go：init 行号表缺失。

## 参数

无随机路径。断言不依赖 `funcN` 的具体编号，只比较 init 命名空间前缀、包含关系与相互不等。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_multi_init_closure_symbols/*.go
GODEBUG=clobberfree=1 /tmp/case
```
