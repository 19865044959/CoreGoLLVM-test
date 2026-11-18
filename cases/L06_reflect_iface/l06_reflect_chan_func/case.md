---
id: l06_reflect_chan_func
dim: L06_reflect_iface
priority: P1
sources:
  - caddy@v2.10.0: replacer_test.go:240 ReplacerFunc provider 链的函数值调用
  - caddy@v2.10.0: replacer_test.go:347 reflect.ValueOf(fn).Pointer() 校验函数身份
  - echo@v4.13.4: echo_test.go:1220 reflect.MethodByName(...).Call 批量注册
  - pgx@v5.7.4: pgconn/ctxwatch/context_watcher_test.go:26 chan 收发取消信号
tags: [L06, reflect, chan, select, MakeFunc, variadic, closure, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

用 `reflect.MakeChan` 创建带缓冲的 `chan *node`，发送后丢弃局部指针，GC 后经 chan 缓冲区 Recv
并解引用；覆盖 TrySend/TryRecv、Close 后 Recv 零值、关闭后 Send/重复 Close 的 panic 路径，以及
`reflect.Select` 的 recv/send/default 选择。再用 `reflect.MakeFunc` 构造捕获 `*node` 的闭包、
变参函数，经 Call/CallSlice/接口调用跨 GC 执行。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_reflect_chan_func`；
2. 缓冲 chan 中的 `*node` 在 GC 后仍可正确读取；Close 后 Recv 返回零值且 ok=false；
3. TrySend/TryRecv 在满/空/关闭场景返回值与 ok 语义正确（空 TryRecv 返回无效 Value）；
4. 关闭后 Send、重复 Close、Call 参数个数错误均 panic 并被 recover；
5. `reflect.Select` 只选就绪分支，满发送通道走 default；关闭通道 recv 返回零值；
6. MakeFunc 闭包捕获的指针、func 值与变参调用在多次 GC 后仍正确，五个 profile 一致。

## 失败判读

- Recv 数据损坏或崩溃：chan 缓冲区的指针未被扫描/写屏障缺失；
- 关闭 chan 行为不符：reflect chan 状态机错误；
- Select 选错分支/deadlock：select 随机就绪判定或 reflect.Select 实现错误；
- MakeFunc 调用崩溃/结果错：闭包环境或 func 值保活失败。

## 参数

- `UAF_SEED`：随机种子，控制消息数量与值；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_chan_func/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
