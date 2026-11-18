---
id: l06_reflect_select_matrix
dim: L06_reflect_iface
priority: P2
sources:
  - pgx@v5.7.4: pgconn/ctxwatch/context_watcher_test.go:26 cancel 信号经 channel 与 select 传递
  - gin@v1.10.0: context_test.go:2352 Stream + CloseNotify 的客户端断开 channel 选择
  - echo@v4.13.4: middleware/timeout_test.go:204 timer 与 handler 竞争的超时选择
tags: [L06, reflect, Select, nil-channel, closed-channel, default, chan-any, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

专测 `reflect.Select` 的边界语义：nil recv/send 永不就绪、closed recv 恒就绪且返回零值、
满 send 通道走 default、仅 default 的 Select、多个 nil 分支中唯一就绪索引、两个 recv 同时就绪的
成员语义、`chan any` 收发、就绪位置 0..3 轮转的固定 5-case 循环，以及错误 Send 类型/缺失 Send/
向已关闭通道发送/非法 Dir 的 panic。接收到的 `*node` 仅由 chan 缓冲区保活，GC 后解引用校验。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_reflect_select_matrix`；
2. nil channel 分支不参与选择，default 被选中；closed recv 返回有效零值且 ok=false；
3. 唯一就绪分支的 chosen 索引确定；多就绪时 chosen ∈ {0,1} 且所有值最终被消费；
4. send 就绪时值入队，满通道不阻塞并走 default；
5. `chan any` 中的指针/字符串接口值经 Select 收发后动态类型正确；
6. 四类非法 SelectCase 均 panic 并被 recover；
7. GC 后经 Select 接收/发送的指针仍在，默认 30 轮在 60s 内完成，支持 `TORTURE_ROUNDS` 放大。

## 失败判读

- `nil recv selects default` / `closed recv ...`：Select 就绪判定错误；
- `single ready recv index` / `varied case position`：case 索引与实际分支错位；
- `received pointer after GC` / `select-sent pointer after GC`：chan 缓冲区未按元素类型扫描；
- `... did not panic`：非法 case 未校验，可能产生未定义行为；
- 超时：Select 阻塞在不可就绪分支，属运行时 bug。

## 参数

- `UAF_SEED`：每轮发送值来源；失败行带出种子；
- `TORTURE_ROUNDS`：轮数（默认 30）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_select_matrix/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=300 UAF_SEED=7 /tmp/case
```
