---
id: l14_iface_value_receiver_method_value
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_iface_value_receiver_method_value/main.go:37 - 值接收者接口实现 ivWorker.Do
  - cases/L14_debug_info/l14_iface_value_receiver_method_value/main.go:81 - ivRun 抓 debug.Stack/CallersFrames
  - cases/L14_debug_info/l14_iface_value_receiver_method_value/main.go:131 - 四类方法值/方法表达式符号断言
  - cases/L14_debug_info/l14_iface_value_receiver_method_value/main.go:145 - 接口方法值绑定值拷贝
  - cases/L14_debug_info/l14_iface_value_receiver_method_value/main.go:156 - 接口方法表达式显式接收者
  - docs/DIMENSIONS.md:22 - L14 调试信息（方法值/接口符号）
tags: [L14, method-value, interface, value-receiver, -fm, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

接口 `ivIface` 的动态类型是**值类型** `ivWorker`（方法为值接收者）。检查接口方法值 `i.Do`（`main.ivIface.Do-fm`）、接口方法表达式 `ivIface.Do`（无 `-fm`）、具体值方法值 `w1.Do`（`main.ivWorker.Do-fm`）与具体方法表达式 `ivWorker.Do`。接口保存赋值时的值拷贝；重新赋值接口变量不改变旧方法值。经方法值 panic 后 traceback 必须落到 `main.ivWorker.Do`。

## 期望行为

1. 四个符号精确为 `main.ivIface.Do-fm`、`main.ivIface.Do`、`main.ivWorker.Do-fm`、`main.ivWorker.Do`；
2. 绑定 `mv := i.Do` 后修改原 worker 并重新赋值接口，旧的 `mv` 仍调用绑定时的值（worker id=1、tags=v1）；
3. `mv` 的 traceback 含 `main.ivWorker.Do(`、`main.ivInvoke(`、goroutine 头与 `main.go:<行号>`；帧表中 `main.ivWorker.Do` 的 File/Line/Entry 可解析；
4. 接口方法表达式 `ivIface.Do(i, q)` 使用接口当前的值（id=2、tags=v2），traceback 同样落到具体值方法；
5. 方法值存入结构体字段后调用仍落到原接收者（id=1）；
6. 显式 GC 后接口值、方法值、结构体字段与 payload 链仍有效；
7. 最后一行 `ALL PASS l14_iface_value_receiver_method_value`。

## 失败判读

- 接口方法值符号缺少 `-fm` 或接口名：接口方法值包装命名错误；
- 绑定后观察到的 receiver id 不是 1：接口方法值捕获了后续接口变量而非创建时的值拷贝；
- 方法表达式得到 id=1：显式 receiver 被忽略；
- traceback 落到分发层或缺 `main.ivWorker.Do(`：接口调用调试信息缺失。

## 参数

无随机路径。`ivInvoke`/`ivRun` 均 `//go:noinline`，保证间接调用帧可见；断言依赖精确符号名与指针身份。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_iface_value_receiver_method_value/*.go
GODEBUG=clobberfree=1 /tmp/case
```
