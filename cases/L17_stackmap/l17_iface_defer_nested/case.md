---
id: l17_iface_defer_nested
dim: L17_stackmap
priority: P2
sources:
  - fasthttp@v1.59.0: server_test.go:2368（hijack 后 user value reset 并触发其 io.Closer）
  - grpc-go@v1.71.1: test/stream_cleanup_test.go:66（流状态发送后才从 activeStreams 删除）
tags: [L17, stackmap, interface, nested-interface, defer, method-expression, nil-receiver, recover, TORTURE_ROUNDS, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 16 轮；TORTURE_ROUNDS 放大轮数，上限 512）"
---

# 场景

嵌套接口 `toucher`（内嵌 `closer`）的 defer 路径：把 `toucher` 装入 `any` 再断言回 `toucher`；先 `defer got.Close(7)`，随后把接口变量重绑定到另一个实现，defer 记录必须仍持有旧 receiver；再用方法表达式 `closer.Close` 注册第二个 defer；闭包 defer 捕获旧接口；recover defer 处理末尾 panic；最后注册 nil receiver 的 `closer`（`(*nilRes)(nil)`）defer。每个方法/defer 内 GC 并校验 receiver 的 head/list/tag。

## 期望行为

1. `any → toucher` 断言成功，`Touch()==23`；
2. defer LIFO：nil Close → recover → 闭包 Touch → `Close(5)` → `Close(7)`，recover 后其余 defer 继续执行；
3. 返回的旧 receiver `calls==2`，list 长度 4，末尾追加值为 105、107（先 5 后 7）；
4. 重绑定后的 `other` receiver 未被调用（`calls==0`）且字段跨 GC 完好；
5. nil receiver 的 `Close` 每轮执行一次，全局计数等于轮数且不崩；
6. 最后一行 `ALL PASS l17_iface_defer_nested`，退出码 0。

## 失败判读

- `Touch/Close: head/list corrupted`：接口 receiver 的指针字段在 GC 后失效；
- `closure-captured toucher corrupted` / `recovered Name corrupted`：闭包或 recover defer 捕获的接口字漏标；
- `Close calls`/`list` 不符：defer 记录中的 receiver 被后续重绑定影响（defer 求值时机错误）；
- `nil receiver` 路径崩溃或计数不符：nil data 的接口 defer 处理异常；
- 仅 clobberfree 下失败：接口 receiver 对象被提前回收。

## 参数

- `UAF_SEED`：失败输出带种子（本轮数据固定，仅作诊断）；
- `TORTURE_ROUNDS=N`：轮数乘 N（默认 1，上限 512），每轮完整跑一遍 defer 链。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_iface_defer_nested/main.go
UAF_SEED=1 TORTURE_ROUNDS=16 GODEBUG=clobberfree=1 /tmp/case
```
