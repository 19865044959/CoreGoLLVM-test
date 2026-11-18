---
id: l14_callers_methods_closures
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_callers_methods_closures/main.go:72 指针接收者方法链
  - cases/L14_debug_info/l14_callers_methods_closures/main.go:80 值接收者方法
  - cases/L14_debug_info/l14_callers_methods_closures/main.go:85 闭包工厂
  - docs/DIMENSIONS.md:22 L14 调试信息（CallersFrames）
tags: [L14, CallersFrames, method-symbol, closure-symbol, pointer-receiver, gc]
requires: []
runtime: "<3s"
---

# 场景

分别通过指针接收者方法、值接收者方法和闭包调用 `runtime.CallersFrames`，核对运行期函数符号：`main.(*stackWorker).methodChain`、`main.stackWorker.valueStep`、`main.makeClosureFunc.func1` 等必须是真实符号，而不是仅含泛化前缀。

## 期望行为

1. 方法链帧精确包含 `main.(*stackWorker).methodChain`、`main.(*stackWorker).pointerStep`、`main.stackWorker.valueStep`、`main.captureFrames`，顺序由内向外成立；
2. 闭包路径包含 `main.makeClosureFunc.func1`、`main.invokeClosure`；
3. 每个被校验帧的 `File` 以 `main.go` 结尾、`Line > 0`，且 `runtime.FuncForPC(f.Entry).Name()` 等于帧函数名（符号真实可解析）；
4. 方法接收者中的 payload 指针在 GC 后有效；
5. 退出码 0，最后一行 `ALL PASS l14_callers_methods_closures`。

## 失败判读

- 方法符号缺少 `(*stackWorker)` 或值/指针接收者混淆：方法符号生成错误；
- 闭包符号不是 `.func1`：闭包符号或内联信息错误（用例通过 noinline 固化闭包工厂）；
- `Entry` 名称与帧名不一致：行号/符号表映射错误；
- 行号为 0：调试信息缺失。

## 参数

无随机路径；所有被断言函数带 `//go:noinline`，保证符号稳定。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_callers_methods_closures/*.go
GODEBUG=clobberfree=1 /tmp/case
```
