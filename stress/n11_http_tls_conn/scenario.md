---
id: n11_http_tls_conn
priority: P1
covers: [L02, L05, L19]
requires: []
default_seconds: 60
max_workers: 64
---

# 场景

HTTP/TLS 连接状态机长跑：`httptest` TLS server + `STRESS_WORKERS` 个并发客户端 worker。客户端共享一个受控 `http.Transport`（`MaxIdleConns`/`MaxIdleConnsPerHost` 受限、短 `IdleConnTimeout`、TLS session cache），持续 POST 确定性 body 并校验响应中的 `id` 与 `crc32`。故障路径按 seed 确定性注入：

- server 对 `hash(id, seed) % 97 ∈ {0,1,2}` 的请求 `Hijack` 后早关、写半截响应或垃圾字节（约 3%）；
- 客户端自定义 `DialContext` 对约 1/8 的新拨号在执行中取消派生 context（cancel-in-dial）；另有约 1/8 的新连接在拨号完成后设置 25ms 的 `SetDeadline`（定时器随后清除，避免污染池化复用），握手或首请求超时即计入 timeout 并重试；
- 每 13 个逻辑请求使用 25ms 的短 context deadline，失败后最多重试 3 次（重试使用不同 id，避免固定命中异常分支）。

独立 GC churn 与心跳 watchdog；结束依次 `CloseIdleConnections`、`srv.Close()`，等待 goroutine 回到基线。

## 不变量

1. 成功响应的 `id` 必须等于本次 attempt 发送的 id，checksum 必须等于请求体的 crc32；非 200 或 body 解析失败立即 FAIL。
2. 请求错误只允许归入预期类别（canceled / deadline / timeout / EOF / reset / malformed）；未知错误立即 FAIL 并打印 seed/worker/id。
3. server 异常行为只由 `hash(id, seed)` 决定，正常请求必须经 server 的 body 校验并回显 checksum；正常路径返回 500 即 FAIL。
4. `Transport` 的 idle 上限固定（每 worker 侧可复用连接数受限），复用/新建连接、dial 取消次数只记录，不做比例或墙钟断言。
5. 短 deadline（context 与 `SetDeadline`）超时与 server 异常只会导致分类计数或有限重试，不会永久卡住 worker；心跳持续增长。
6. 关闭顺序为：worker 全部返回 → 停辅助 goroutine → `CloseIdleConnections` → `srv.Close`；goroutine 数回到基线 +4 以内。
7. 至少有一次成功响应通过校验；最后打印 metrics 与 `STRESS PASS n11_http_tls_conn`。

## 参数

- `STRESS_SECONDS`（默认 60，`0`=固定轮数模式）
- `STRESS_WORKERS`（默认 8，上限 64）
- `STRESS_HEARTBEAT`（默认 5s，6 个周期无进展判 FAIL）
- `UAF_SEED`（默认 1，决定 server 异常分支与 dial 取消位置）
- `STRESS_FIXED_ROUNDS`（默认 32，仅 `STRESS_SECONDS=0` 时生效；每 worker 的逻辑请求数）

## 失败判读

- `checksum mismatch` / `malformed success body` / `expected=HTTP 200`：成功路径数据或协议状态损坏；
- `unclassified request error`：出现预期集合之外的错误（TLS/Transport/netpoll 状态机可疑）；
- `watchdog stalled`：TLS 握手或连接池死锁、netpoll 饥饿；
- `goroutine leak`：persistConn / TLS 握手 goroutine 未退出；
- 仅在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress FILTER=n11 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=n11 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n11 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
SUITE=stress FILTER=n11 STRESS_SECONDS=3 PROFILES=soak,soak-single REPEAT=1 ./run.sh
```

## 来源

- `go/src/net/http/transport_test.go:782-832,1864-1937,3953-4033`（连接池复用 / idle race / 早关）
- `go/src/crypto/tls/handshake_client_test.go:2269-2320`（TLS 握手竞争）
- `go/src/net/dial_test.go:77-178`（并发 dial / cancel）
- <https://github.com/dotnet/runtime/tree/main/src/libraries/System.Net.Http/tests/StressTests/HttpStress>（HTTP/TLS 独立压力工程形态）
