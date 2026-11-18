---
id: l14_funcval_interface_dispatch
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_funcval_interface_dispatch/main.go:58 两个 panic 目标函数
  - cases/L14_debug_info/l14_funcval_interface_dispatch/main.go:77 命名函数类型存于 any 后断言调用
  - cases/L14_debug_info/l14_funcval_interface_dispatch/main.go:90 map[string]any 函数表按键分发
  - cases/L14_debug_info/l14_funcval_interface_dispatch/main.go:110 符号解析、重绑定与 traceback 断言
  - docs/DIMENSIONS.md:22 L14 调试信息（函数指针帧）
tags: [L14, func-value, interface-dispatch, named-func-type, func-table, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

函数指针以命名函数类型 `fvFunc` 存入 `any` 与 `map[string]any`，调用方经类型断言取得后间接调用；另在运行期把函数变量从 alpha 重绑定到 beta。panic 后的 traceback 必须落到真实目标函数符号，而不是接口断言/分发层；`reflect.ValueOf(v).Pointer()` + `runtime.FuncForPC` 必须反查出与目标一致的真实符号。

## 期望行为

1. `any` 中命名函数类型的 Kind 为 `func`，符号为 `main.fvLeafAlpha`；
2. 接口分发 panic 的 traceback 包含 `main.fvLeafAlpha(`、`main.fvRunAny(`、`goroutine` 头与 `main.go:<行号>`；
3. map 函数表按 key 分发，traceback 只含被选中的 `main.fvLeafBeta(`，不含 `main.fvLeafAlpha(`，并含 `main.fvRunMap(` 与行号；
4. 运行期重绑定后调用落到 `main.fvLeafBeta`，traceback 不含旧目标；
5. 零值命名函数类型 == nil；
6. 两次 GC 后 payload 指针链与 map 中函数符号仍有效；
7. 退出码 0，最后一行 `ALL PASS l14_funcval_interface_dispatch`。

## 失败判读

- traceback 目标是分发函数而非被调用函数：间接调用帧调试信息缺失；
- map 分发同时出现两个目标：函数表符号/调用路径错误；
- 重绑定后仍显示旧符号：函数变量符号表未随赋值更新；
- payload 失效：接口/函数帧栈映射漏标。

## 参数

无随机路径；`UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_funcval_interface_dispatch/*.go
GODEBUG=clobberfree=1 /tmp/case
```
