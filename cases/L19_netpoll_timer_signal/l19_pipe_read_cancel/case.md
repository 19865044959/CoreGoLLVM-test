---
id: l19_pipe_read_cancel
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - caddytest@v2.10.0: caddytest/integration/stream_test.go:22 io.Pipe 写 body、flush 后读响应，流式连接关闭不卡死
  - grpc-go@v1.71.1: internal/transport/handler_server_test.go:250 HTTP handler 使用 io.Pipe 传递请求 body，取消时读端必须被释放
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:125 Close 后写入返回错误，关闭前已入队数据仍可读出
tags: [L19, io-pipe, os-pipe, read-cancel, closewitherror, eof, sticky-error, pointer, gc, UAF_SEED]
requires: []
runtime: "<1s"
---

# 场景

模拟管道读取被取消的三条路径：`io.Pipe` 读到部分数据后 `CloseWithError`，
阻塞读必须带着指定错误返回且错误粘滞、二次关闭不覆盖；`os.Pipe` 关闭写端时
阻塞读得到 `io.EOF`；另一 goroutine 关闭读端时阻塞读得到 `os.ErrClosed`。
每次取消前后显式 GC，并校验调用方栈上的指针链。

## 期望行为

1. `io.Pipe` 第一次读拿到完整 8+4 字节 payload 且 err 为 nil；
2. `CloseWithError(errCanceled)` 后第二次读满足 `errors.Is(err, errCanceled)`，
   后续读返回同一错误；
3. 第二次 `CloseWithError` 返回 nil，且不覆盖首个错误；
4. `os.Pipe` 写端关闭后阻塞读返回 `io.EOF`；
5. 另一 goroutine 关闭读端后阻塞读返回满足 `errors.Is(err, os.ErrClosed)` 的错误；
6. 最后一行输出 `ALL PASS l19_pipe_read_cancel`，失败退出码为 1。

## 失败判读

- `first read n=...`：`io.Pipe` 无缓冲读写配对错误；
- `want sentinel` / `sticky error`：`CloseWithError` 错误未传播或被覆盖；
- `os.Pipe read after ...`：管道关闭未唤醒阻塞读，或错误类型不对；
- 超时 5s：读端/写端互相等待；
- payload 损坏：取消路径上的栈扫描错误。

## 参数

- `UAF_SEED`：随机路径种子，参与 payload 基值计算。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_pipe_read_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
