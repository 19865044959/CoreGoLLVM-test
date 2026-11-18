---
id: l19_conn_deadline_exceeded
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/client_test.go:1945 客户端写 deadline 设为 1ns 制造 slow consumer，错误必须可判别
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 的写 deadline 返回 os.ErrDeadlineExceeded
  - etcd@v3.5.0: client/pkg/transport/timeout_dialer_test.go:23 回环 TCP 读写超时语义
tags: [L19, deadline, deadline-exceeded, tcp, net-pipe, netpoll, pointer, gc, UAF_SEED]
requires: [net]
runtime: "<2s"
---

# 场景

模拟连接 deadline 到期与恢复：TCP 回环连接设置已过期的读 deadline，连续两次
`Read` 必须返回 timeout 且满足 `errors.Is(err, os.ErrDeadlineExceeded)`；清空
deadline 后同一连接仍需正常读到服务器帧。随后验证过期写 deadline 不阻塞，以及
`net.Pipe` 上的读/写 deadline 行为一致。全程不做耗时断言，只用绝对 deadline。

## 期望行为

1. 过期读 deadline 下两次 `Read` 都返回 `net.Error` 且 `Timeout()==true`，
   并满足 `errors.Is(os.ErrDeadlineExceeded)`；
2. deadline 错误不得被误判为 `io.EOF`；
3. `SetReadDeadline(time.Time{})` 清空后同一 TCP 连接可完整读取帧，GC 后
   payload 指针链完好；
4. 过期写 deadline 下 `Write` 立即返回 timeout；清空后可正常写入 ACK；
5. `net.Pipe` 的读/写 deadline 与 TCP 语义一致；
6. 最后一行输出 `ALL PASS l19_conn_deadline_exceeded`，失败退出码为 1。

## 失败判读

- `not a timeout` / `not ErrDeadlineExceeded`：netpoll deadline 未映射到正确错误；
- 清空 deadline 后读取超时：deadline 状态未复位；
- `deadline error reported as EOF`：把超时误转为 EOF；
- 写 deadline 未生效导致阻塞：写路径绕过 deadline 检查；
- payload 损坏：deadline 唤醒路径上的栈/指针处理错误。

## 参数

- `UAF_SEED`：随机路径种子，参与帧基值计算。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_conn_deadline_exceeded/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
