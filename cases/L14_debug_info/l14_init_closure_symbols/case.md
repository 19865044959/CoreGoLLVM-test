---
id: l14_init_closure_symbols
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_init_closure_symbols/main.go:34 - 包级函数字面量 initCloOne
  - cases/L14_debug_info/l14_init_closure_symbols/main.go:37 - 包级闭包工厂 initCloFactory
  - cases/L14_debug_info/l14_init_closure_symbols/main.go:68 - 方法作用域闭包 build
  - cases/L14_debug_info/l14_init_closure_symbols/main.go:135 - 符号断言
  - docs/DIMENSIONS.md:22 - L14 调试信息（符号/行号）
tags: [L14, closure, init, symbol, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

包级函数字面量在 init 阶段被创建：`initCloOne` 的符号是 `main.init.funcN`，闭包工厂 `initCloFactory` 返回的内层闭包挂在工厂符号下（`main.init.funcN.1`）；方法内返回的闭包符号是 `main.(*icHolder).build.func1`。三类闭包 panic 后 recover 并抓栈，验证 init/方法作用域闭包的真实符号与行号。对应框架在 init 阶段注册回调、运行期崩溃时定位闭包来源的场景。

## 期望行为

1. 两个包级闭包的运行期符号都以 `main.init.` 开头且互不相同；
2. 工厂产出的内层闭包符号以工厂符号加 `.` 为前缀，但自身独立；
3. 方法作用域闭包符号精确为 `main.(*icHolder).build.func1`，三类闭包符号两两不同；
4. 三类闭包 panic 的 traceback 都包含各自真实符号和 `main.go:<行号>`，recover 成功；
5. 每个帧 File 以 `main.go` 结尾、Line>0，`runtime.FuncForPC(Entry).Name()` 与帧名一致；
6. init 期创建的闭包在 main 阶段调用后仍保留 init 符号；
7. GC 后闭包捕获的 payload 与链仍有效，最后一行 `ALL PASS l14_init_closure_symbols`。

## 失败判读

- 包级闭包符号不是 `main.init.*`：init 函数命名/符号表错误；
- 内层闭包符号等于工厂符号：嵌套闭包符号重复；
- 方法闭包符号丢失接收者或方法名：方法作用域符号错误；
- 行号为 0 或 File 非 main.go：init/闭包行号表缺失。

## 参数

无随机路径。断言不依赖 `funcN` 的具体编号值，只比较前缀、相互关系和精确的方法闭包名。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_init_closure_symbols/*.go
GODEBUG=clobberfree=1 /tmp/case
```
