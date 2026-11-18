---
id: l14_nested_closure_symbols
dim: L14_debug_info
priority: P0
sources:
  - cases/L14_debug_info/l14_nested_closure_symbols/main.go:60 外层闭包内嵌套 IIFE 闭包 panic
  - cases/L14_debug_info/l14_nested_closure_symbols/main.go:69 返回“外层闭包 -> 内层闭包”的二级工厂
  - cases/L14_debug_info/l14_nested_closure_symbols/main.go:80 recover 抓栈
  - cases/L14_debug_info/l14_nested_closure_symbols/main.go:96 两级 funcval 符号与 traceback 断言
  - docs/DIMENSIONS.md:22 L14 调试信息（闭包符号）
tags: [L14, nested-closure, closure-symbol, funcval, reflect, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

闭包工厂返回外层闭包，外层闭包再创建内层闭包（含嵌套 IIFE），模拟框架中层叠 handler/回调的符号链。运行期必须能分别反查 `main.nestedPanicFactory.func1` 与 `main.nestedPanicFactory.func1.1`；内层闭包 panic 后，traceback 必须同时包含两级闭包符号、宿主函数符号与 `main.go:行号`。另用二级计数器工厂验证同一工厂的闭包共享符号、不同实例捕获状态互相独立。

## 期望行为

1. 同工厂两个外层闭包的 funcval 符号均为 `main.nestedAdderFactory.func1`；
2. 两个内层闭包的 funcval 符号均为 `main.nestedAdderFactory.func1.1`；
3. 两个内层计数器独立累加（11/13 与 101/103）；
4. panic traceback 同时包含 `main.nestedPanicFactory.func1.1(`、`main.nestedPanicFactory.func1(`、`main.nestedRun(`、`goroutine` 头与 `main.go:<行号>`；
5. recover 到的 payload 与 panic 时同一对象，两次 GC 后 id/name/next 链仍有效；
6. 退出码 0，最后一行 `ALL PASS l14_nested_closure_symbols`。

## 失败判读

- 内层符号不是 `.func1.1` 或与外层同名：嵌套闭包符号命名/符号表错误；
- traceback 缺任一级闭包帧：内联/闭包帧展开错误；
- 计数器状态串扰：闭包环境捕获（按引用）实现错误；
- 捕获 payload 在 GC 后失效：闭包环境变量栈映射漏标。

## 参数

无随机路径；`UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_nested_closure_symbols/*.go
GODEBUG=clobberfree=1 /tmp/case
```
