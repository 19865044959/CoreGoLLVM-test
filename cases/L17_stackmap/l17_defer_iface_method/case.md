---
id: l17_defer_iface_method
dim: L17_stackmap
priority: P0
sources:
  - nats-server@v2.11.0: server/auth_test.go:372（net.Conn 接口上的 defer conn.Close()）
  - grpc-go@v1.71.1: clientconn_test.go:76（net.Listener 接口上的 defer lis.Close()）
tags: [L17, stackmap, defer, interface-method, itab, deferreturn, GC]
requires: []
runtime: "<3s（默认 10 轮）"
---

# 场景

defer 记录保存 interface 字（itab + data），执行到 deferreturn 才通过 itab 调用方法。用例覆盖四种写法：`defer c.Close()` 直接接口方法调用；defer 闭包调用 `c.Close()` 并累加返回值；interface 作为 defer 实参复制进 defer 记录；接口方法表达式 `f := closer.Close` 后在闭包中调用。`res`（指针 receiver）与 `vres`（值 receiver）两种实现均在方法内 GC 并校验自己的指针字段。

# 期望行为

1. 四种路径的累加和分别等于按 head/tail 计算的期望值；
2. `res.calls` 恰好记录 4 次 Close 调用；
3. 所有 defer 执行完再 GC，`res.head/list` 与 `vres` 原值不变；
4. 最后一行 `ALL PASS l17_defer_iface_method`，退出码 0。

# 失败判读

- 求和错误：defer 记录中的 interface data 或接收者副本未被扫描；
- `res.Close/vres.Close: head corrupted`：方法调用时 receiver 指针已失效；
- `close calls` 数量错误：defer 执行顺序/次数异常；
- 仅 clobberfree 下失败：defer 记录引用的对象被提前回收；
- 崩溃：deferreturn 经 itab 调用的栈映射错误。

# 参数

- `UAF_SEED`：决定 base 偏移与 list 长度；失败信息带种子。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_defer_iface_method/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
