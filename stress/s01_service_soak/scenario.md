---
id: s01_service_soak
priority: P1
covers: [L02, L05, L06, L17, L18, L19]
requires: []
default_seconds: 60
max_workers: 64
---

# 场景

进程内 HTTP/JSON 服务长跑：`httptest.Server` 暴露 `POST /work`，`STRESS_WORKERS` 个客户端 worker 持续发送确定性请求（`id/name/vals` 由 seed+worker+round 推导）。handler 解码并校验请求，计算 sum，写入服务端缓存（`map[int64]*record` + 环形 `[]*record` 按容量淘汰 + `interface{}` 视图），按请求间隔显式 `runtime.GC()`，再返回 JSON；客户端逐请求校验 `id/name/sum/n`。独立 goroutine 周期 GC 并全量校验缓存；阶段推进 warmup → steady → shrink（丢弃一半缓存 + `debug.FreeOSMemory()`）→ regrow，收尾做最终一致性校验。

## 不变量

1. 每个响应 `id/name/sum/n` 与请求推导值一致（客户端持续校验）；
2. handler 解码校验请求，入库后（锁内）立即校验 map 指针与 record 内容一致；
3. 缓存中每个有效 record 的 `sum` 可由 `vals` 重算，`interface{}` 视图为 `[id,sum]`，name 形如 `s01-w*`；
4. 环形缓存长度不超过容量，ring 中的 record 可在 map 索引到，map 中的 record 由 ring 支撑；
5. shrink 丢弃一半后，存活 record 内容与 map/ring 一致性不变；regrow 阶段缓存重新增长；
6. 客户端、服务端与 verifier 的请求数/校验数只记录，不做墙钟/内存阈值断言；
7. 心跳随每个完成的请求增长，watchdog 在 6×H 无进展时 FAIL 并打印全部 goroutine 栈；
8. 关闭顺序为停生产（worker 全部返回）→ 停辅助 goroutine → drain（`server.Close`），goroutine 数回到基线；
9. 结束打印 metrics 行与 `STRESS PASS s01_service_soak`。

## 参数

- `STRESS_SECONDS`（默认 60，`0`=固定轮数模式）
- `STRESS_WORKERS`（默认 8，上限 64）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`（默认 1，影响 vals/name 派生值）
- `STRESS_FIXED_ROUNDS`（默认 32，仅 `STRESS_SECONDS=0` 时生效；每 worker 的轮数）

缓存容量固定为 `32 + 12*workers`（默认 128），用于制造按容量淘汰。

## 失败判读

- `response id/name/sum/n mismatch`：响应 JSON 字段被破坏或客户端/服务端共享状态错乱；
- `post-insert check` / `cached ...`：入库后 record 内容或 map 指针被破坏；
- `ring size/map size/backed by ring`：缓存结构（slice/map/环）损坏；
- `cache consistent after shrink` / `final cache consistent`：FreeOSMemory 后存活对象被错误回收或内容破坏；
- `handler ... decode error` / `HTTP 200 expected`：请求编解码或服务端处理路径损坏；
- `watchdog stalled`：死锁、GC 饥饿或网络请求永久卡死；
- `goroutine leak`：关闭流程未 drain 干净；
- 仅在 clobberfree/efence 下失败：UAF。

## 运行

```bash
SUITE=stress FILTER=s01 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=s01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=s01 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
SUITE=stress FILTER=s01 STRESS_SECONDS=3 PROFILES=soak,soak-single REPEAT=1 ./run.sh
```
