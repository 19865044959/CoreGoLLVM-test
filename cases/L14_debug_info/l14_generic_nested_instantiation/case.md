---
id: l14_generic_nested_instantiation
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_generic_nested_instantiation/main.go:63 - 泛型叶子 gnInner[T] 用 %T 标记实例化类型
  - cases/L14_debug_info/l14_generic_nested_instantiation/main.go:71 - gnMid[T] 固定调用 gnInner[string]
  - cases/L14_debug_info/l14_generic_nested_instantiation/main.go:74 - gnOuter[T] 的 recover 抓 debug.Stack/CallersFrames
  - cases/L14_debug_info/l14_generic_nested_instantiation/main.go:146 - 断言 outer[int] 到达 string 实例化
  - cases/L14_debug_info/l14_generic_nested_instantiation/main.go:153 - 三个实例化符号各出现一次
  - docs/DIMENSIONS.md:22 - L14 调试信息（泛型实例化/栈展开）
tags: [L14, generics, instantiation, nested, traceback, CallersFrames, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

泛型调用图 `gnOuter[int] -> gnMid[int] -> gnInner[string]`：外层与中层用 int 实例化，叶子固定用 string 实例化。叶子 panic 的 payload 带 `fmt.Sprintf("%T", zero)` 类型标记；`gnOuter[int]` 的 defer recover 后抓 `debug.Stack()` 与 `runtime.CallersFrames`。对应泛型库在一个 traceback 中混有多个不同类型实例化帧时的符号与行号定位。

## 期望行为

1. `gnInner[int]`、`gnInner[string]`、`gnMid[int]`、`gnMid[string]`、`gnOuter[int]` 的运行期符号都带 `[...]` 方括号；int/string 的 funcval 入口指针不同；
2. 从 `gnOuter[int]` 进入后，payload 的类型标记为 `string`，证明叶子调用的是 `gnInner[string]` 而非 outer 的 int 实例化；
3. 同一个 `debug.Stack()` 同时包含 `main.gnInner[...](`、`main.gnMid[...](`、`main.gnOuter[...](` 三个主函数帧，各恰好一次（recover 闭包另计）；
4. `CallersFrames` 中三个泛型逻辑帧的 File 以 `main.go` 结尾、Line>0，`FuncForPC(Entry).Name()` 与帧名一致；
5. 行号满足 `gnInner` panic 行 < `gnMid` 调用 string 实例化行 < `gnOuter` 调用行；
6. 显式 GC 后 payload 与后继链仍有效，最后一行 `ALL PASS l14_generic_nested_instantiation`。

## 失败判读

- 符号含 `[`/`]` 但类型标记不是 `string`：嵌套实例化调用目标错误；
- 同一 traceback 缺少 `gnInner`/`gnMid`/`gnOuter`：泛型帧展开或符号表缺失；
- int/string funcval 指针相同：实例化入口复用错误；
- 行号顺序颠倒或为 0：泛型内联/行号表归属错误。

## 参数

无随机路径。所有泛型函数均 `//go:noinline`，保证逻辑帧出现在 traceback；断言只比较符号前缀/后缀、入口差异与相对行号，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_generic_nested_instantiation/*.go
GODEBUG=clobberfree=1 /tmp/case
```
