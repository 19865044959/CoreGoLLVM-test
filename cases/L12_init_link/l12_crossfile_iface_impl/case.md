---
id: l12_crossfile_iface_impl
dim: L12_init_link
priority: P2
sources:
  - fiber@v2.52.6: router_test.go:306 var app interface{} = (*App)(nil) 后断言 Router
  - caddy@v2.10.0: context_test.go:22 模块实例化后断言为 io.Writer
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 Compressor 接口注册表
tags: [L12, interface, cross-file-methods, method-set, embedding, typed-nil, itab, gc, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 2000 次派发 × 3 实现；TORTURE_ROUNDS 放大）"
---

# 场景

接口 `Transport`/`Closer`/嵌入两者的 `TransportCloser` 定义在 `iface.go`；`tcpTransport` 的 Name/Send/Close 与 Stats 分别位于 `tcp.go` 和 `tcp_stats.go`（跨文件方法集）；`udpTransport` 与 typed-nil 的 `nilTransport` 在另外文件。全局 `map[string]Transport` 在包变量初始化阶段构建。主程序验证接口相等、接口转换、方法表达式、type switch、typed nil 语义，并在种子轮换的派发循环与并发只读 + GC 下复核。

# 期望行为

1. 注册表名字排序为 `nil,tcp,udp`；同一接口值自比相等，不同实现不相等，typed-nil 接口本身非 nil；
2. `tcp` 实现 `TransportCloser`：发送 5 字节后 Stats 为 `{5,0}`，首次 Close 返回 nil，再次 Close 返回 `errClosed`，关闭后发送返回 -1 且 Failed=1；
3. `udp` 发送 3 字节、关闭语义与 Stats 正确；
4. typed nil：断言得到 nil 指针，`Name/Stats/Close` 可用，`Send` panic 被 recover，接口等于 `Transport((*nilTransport)(nil))`；
5. 接口转换到 `named` 成功，缺失方法的断言失败；
6. 方法值 `tcp.Name`、方法表达式 `Transport.Name`/`Transport.Stats` 结果正确；
7. type switch 优先命中具体类型，`nilTransport` 落入 `Transport` 分支，非接口值落入 default；
8. 种子轮换的 2000×rounds 次派发结果全部正确；8 goroutine 并发 `Name/Stats` + GC 无错误；
9. GC 后注册表接口值、tcp 统计与 payload 指针保持不变；最后一行 `ALL PASS l12_crossfile_iface_impl`。

# 失败判读

- 编译失败 `does not implement`：跨文件方法集/嵌入接口实现错误；
- typed-nil 接口被判定为 nil：接口数据字/类型字语义错误；
- type switch 命中错误分支：itab 或具体类型判定错误；
- GC 后接口派发失败：接口数据指针或 itab 扫描错误；
- 并发阶段统计变化：只读路径出现隐藏写入；
- 失败行 `seed=` 可用于复现派发起点。

# 参数

- `TORTURE_ROUNDS`（默认 1，上限 32）：派发循环次数 = `2000×rounds`（上限 32000），并发每 worker = `100×rounds`；
- `UAF_SEED`：决定派发与并发读取的起始实现下标。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_crossfile_iface_impl/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=8 UAF_SEED=5 /tmp/case
```
