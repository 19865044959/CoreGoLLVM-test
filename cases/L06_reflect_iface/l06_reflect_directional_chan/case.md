---
id: l06_reflect_directional_chan
dim: L06_reflect_iface
priority: P1
sources:
  - caddy@v2.10.0: replacer_test.go:240 ReplacerFunc provider 链的函数值调用
  - caddy@v2.10.0: replacer_test.go:347 reflect.ValueOf(fn).Pointer() 校验函数身份
  - pgx@v5.7.4: pgconn/ctxwatch/context_watcher_test.go:26 cancel 信号经 channel 收发
  - yaegi@v0.16.1: interp/interp_eval_test.go:653 解释代码经 reflect.Value.Call 调用宿主函数
tags: [L06, reflect, ChanOf, chan-direction, FuncOf, MakeFunc, select-direction, nil-func, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

动态装配代码常用 `reflect.ChanOf` 构造方向化通道类型，把已有双向通道转换成 `chan<- T`/`<-chan T`
后交给发送端/接收端，并用 `reflect.FuncOf`+`reflect.MakeFunc` 生成签名在编译期未知的回调。
本用例覆盖方向类型身份、方向受限的 Send/Recv/Try*/Select、关闭语义，以及动态函数签名和 nil func
在显式 GC 后的可调用性。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l06_reflect_directional_chan`；
2. `ChanOf(BothDir/SendDir/RecvDir, T)` 与字面 `chan T`/`chan<- T`/`<-chan T` 类型完全相等，
   `ChanDir`/`Elem`/`ConvertibleTo`/`AssignableTo` 矩阵符合 Go 转换规则；
3. 双向通道值可 Convert 为方向通道；反向不可转换；方向不匹配的 Recv/Send/TryRecv/TrySend
   与 SelectSend/SelectRecv 均 panic；
4. 经 send-only 视图发送的指针元素在 GC 后仍可从 recv-only 视图完整读出；
5. `FuncOf` 动态签名与字面函数类型相等，变参标志正确；`MakeFunc` 结果经反射和接口类型断言在
   GC 后均可调用；
6. nil func 的 `IsNil`/装箱语义正确，调用 panic；函数值经方向通道传递后仍可调用；
7. clobberfree/efence/stress 等 profile 下输出一致。

## 失败判读

- `ChanOf ... matches literal type`：方向通道类型构造或类型缓存错误；
- `bidirectional converts ...` / `directional cannot convert back`：可转换性判定错误；
- `... did not panic`：方向约束或 nil func/Select 方向检查缺失；
- `recv-only ... after GC`：通道元素未被扫描，跨 GC 保活失败；
- `dynamic func after GC`：MakeFunc 闭包捕获的指针/函数值未被保守扫描。

## 参数

- `UAF_SEED`：决定每轮元素值与 MakeFunc 偏移；失败输出带出该种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_directional_chan/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
