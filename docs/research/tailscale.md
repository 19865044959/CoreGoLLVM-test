# tailscale v1.80.2

- 来源：`research/repos/tailscale`（partial clone，仅 `*_test.go` / `testdata/` / `go.mod` / 必要测试辅助源码）
- 模块：`tailscale.com`，`go 1.23.1`；测试使用 `quicktest`、`tstest` 假时钟、`memnet` 内存连接与 `pion` 之外的少量 `x/sys`，网络/平台相关包普遍带 `_linux`/`_windows` 构建标签。
- 本地路径：`research/repos/tailscale`；HEAD `c7a79d7b`，`git describe --tags` = `v1.80.2`。
- 范围：仓库内 435 个 `*_test.go`。按 `concurrent|goroutine|leak|stress|shutdown|timeout|deadline|cancel|timer|atomic|mutex|select|channel|pool|reflect|interface|state|watch` 过滤后精读 21 个文件：`syncs`、`types/lazy`、`net/{connstats,flowtrack,memnet,dnscache,art}`、`derp`、`control/controlbase`、`ipn/ipnlocal`、`ipn/ipnserver`、`health`、`logtail`、`wgengine/watchdog`。文件:行均相对本仓根目录。
- 说明：`syncs`、`types/lazy`、`net/connstats`、`net/flowtrack`、`net/memnet`、`net/dnscache`、`net/art`、`derp`（用 memnet 替代 TCP）、`control/controlbase`（用内存 pipe）、`ipnlocal/bus`、`health`、`logtail` 的核心机制均落在纯 Go 内存逻辑；`ipnserver` 的并发用户切换测试依赖 Windows 令牌，只借鉴并发结构。

## 场景表

| 测试文件:行 | 场景摘要 | 维度 | 复杂度 | stdlib 重写思路 |
|---|---|---|---|---|
| syncs/shardedmap_test.go:8 | 16 shard 的泛型 map：Set 返回是否新增、Delete 返回是否存在、Mutate 三态（新增/修改/删除） | L02/L01/L13 | 中 | `[16]struct{mu; map}` + shard 函数；Mutate 在单 shard 锁内执行回调；表驱动覆盖三态 |
| syncs/syncs_test.go:145 | 容量 2 的 Semaphore：Acquire/TryAcquire/Release，已取消 ctx 的 AcquireContext 立即失败 | L02/L04 | 低 | buffered chan 或计数+cond；覆盖超限、取消、释放后恢复 |
| syncs/syncs_test.go:167 | 泛型 Map 的 Load/Store/LoadOrStore/LoadOrInit/All/Delete；两个 goroutine 并发 LoadOrStore 恰有一个返回 loaded=false | L01/L02/L06 | 中 | `sync.Map` 泛型包装 + 方法集；并发测试用 start barrier 提高碰撞概率 |
| syncs/shardvalue_test.go:16 | 每 CPU 一个 shard 的计数聚合；1000 goroutine × 10000 次 `One(Add(1))`，总和必须等于 10^7，分布近似均匀 | L02/L03/L13 | 高 | `runtime.NumCPU()` 个 padded 原子计数器 + 原子 index；长循环穿插 GC；汇总校验 |
| types/lazy/sync_test.go:127 | 10000 goroutine 同时首次 `Get`，初始化函数只执行一次，所有 goroutine 得到 42 | L02/L04/L06 | 中 | `sync.Once` + atomic 指针；start channel 同时放行；用计数验证 init 次数为 1 |
| net/connstats/stats_test.go:50 | 统计器按 maxPeriod 周期回调 dump；即使无新增流量也必须至少触发一次 | L19/L02 | 中 | `time.Ticker` + 后台 goroutine；`select` 等 dump 或超时；Shutdown 关闭 ticker |
| net/connstats/stats_test.go:82 | `NumCPU` 个 goroutine 各发 1000 个随机虚拟包，周期回调聚合 virtual 计数；每连接计数不得超 maxConns | L02/L03/L05 | 高 | 分片计数 map + 定期合并；高并发生成/更新，GC 穿插；校验总包数守恒 |
| net/flowtrack/flowtrack_test.go:15 | 五元组 LRU cache：MaxEntries=2，超限淘汰最旧；Remove/Get 零分配 | L13/L19 | 中 | map + 双向链表/list + `sync.Mutex`；`testing.AllocsPerRun` 校验 Get 无分配 |
| net/memnet/pipe_test.go:33 | 内存 pipe 的读/写 deadline；`Block` 后写/读必须按 deadline 超时而非永久阻塞 | L19/L04 | 中 | `sync.Cond` + 计时器；Block 置位后由 timer 广播唤醒；错误统一 `os.ErrDeadlineExceeded` |
| net/memnet/pipe_test.go:83 | 容量 1 的 pipe：写 3 字节的 goroutine 被阻塞，读者逐字节读取后写完成，n=3 | L02/L04/L13 | 中 | 有界队列 + cond；写按容量分段；读者慢速消费，校验写入总量与阻塞解除 |
| net/dnscache/messagecache_test.go:20 | DNS 缓存：TTL 递减、过期 miss、size 上限 2、查询 ID 重写、按 type/class 区分 | L19/L13/L05 | 中 | map + 假时钟；entry 存过期时间与原始报文；命中时复制并改写 TxID；LRU 淘汰 |
| derp/derp_test.go:53 | 3 个客户端经真实 TCP 连 DERP server，广播收发、preferred 标记、peer-gone 计数 | L02/L04 | 高 | 用 `net.Pipe`/内存连接替换 TCP；server 端 per-client 读/写 goroutine + 路由表；校验广播与离线通知 |
| derp/derp_test.go:272 | 3 客户端 alice/bob/cathy；cathy 停止读取导致其队列积压，alice→bob 必须继续通畅；WriteTimeout 100ms | L02/L04/L19 | 高 | 每目标独立有界发送队列 + 丢弃/超时策略；一个慢消费者，定时器驱动，校验快路径不受影响 |
| derp/derp_test.go:863 | 一个 goroutine 循环 Add/Remove packet forwarder，同时主 goroutine 发 5000 个包，全部转发且无锁竞态 | L02/L04 | 高 | `RWMutex` 保护 forwarder 映射 + 原子替换；并发热更新与转发计数 |
| derp/derp_test.go:1522 | 发送端 token bucket：无限速时 1000 次写全通过；设 1 B/s + burst 后写次数与字节数被限制 | L19/L03 | 中 | `golang.org/x/time/rate` 等价的令牌桶 stdlib 实现（时间戳+令牌数）；用假时钟推进，统计写调用次数 |
| control/controlbase/conn_test.go:118 | 15KiB 数据跨多个 Noise 帧收发，双向内容一致，tx/rx nonce 严格同步推进 | L13/L05 | 中 | 分块长度为上限的 frame 编解码 + 顺序 nonce 计数；内存 pipe；校验 chunk 数与 nonce |
| control/controlbase/conn_test.go:234 | 建立 1000 个连接各起一个阻塞读 goroutine；`runtime.GC` 后堆增长 < 2KB/连接；关闭后 goroutine 全部退出 | L04/L18/L19 | 高 | 每连接固定小缓冲 + 阻塞 read；GC + `runtime.ReadMemStats` 统计；关闭后轮询 `NumGoroutine` |
| ipn/ipnlocal/bus_test.go:22 | 用 reflect 枚举 `ipn.Notify` 全部字段，逐字段构造非零值并验证 `isNotableNotify` 判定 | L06 | 中 | `reflect.TypeOf/ValueOf` 遍历字段，按 Kind 填值；验证新增字段未被漏判 |
| ipn/ipnlocal/bus_test.go:123 | 限速 bus：首条立即发送，窗口内后续 Notify 合并为最新值，假时钟推进后 flush | L19/L03/L02 | 高 | 假时钟 + last-value 覆盖缓冲 + flush timer；Run 循环 `select` 收包/定时/取消 |
| ipn/ipnserver/server_test.go:191 | 10 轮 × 100 goroutine 并发连接/断开不同“OS 用户”，每次断开后当前用户必须清空 | L02/L04 | 高 | 引用计数会话表 + 关闭回调；100 并发 acquire/release；每轮结束断言计数为 0、无泄漏 |
| health/health_test.go:111 | 注册/注销 watcher，unhealthy/healthy 状态变化按序回调，注销后不再收到 | L02/L19 | 中 | 观察者列表 + 每 watcher 队列；加锁复制列表后回调；channel 断言 |
| health/health_test.go:173 | Warnable 配 TimeToVisible=2s：状态立即置 unhealthy，但 watcher 1s 时未通知，满足时长后才通知 | L19 | 中 | 状态 + 首次不健康时间戳 + 延迟 timer；假时钟推进验证可见性 |
| logtail/logtail_test.go:24 | 已取消 ctx 上 `Shutdown` 立即返回错误/完成，不阻塞 | L19/L02 | 低 | `select` ctx.Done 与关闭完成；确保资源清理 |
| logtail/logtail_test.go:77 | 写入 3 行日志后 Shutdown，后台必须排空所有 pending 日志再退出 | L19/L05 | 中 | 有界缓冲 + 后台 flush goroutine；Shutdown 关闭输入并等待 drain；校验总行数 |
| wgengine/watchdog_test.go:15 | 健康引擎连续 `RequestStatus` 后 Close，watchdog 定时器不得误报超时 | L19/L04 | 中 | 每次操作重置 timer + 超时回调；Close 停 timer；用短超时验证不触发 |
| net/art/table_test.go:578 | 1 万个前缀建 ART，1 万次随机地址查找与慢速参考实现逐项比对 | L03/L13/L17 | 高 | 简化 radix/prefix trie + 慢速线性参考；长循环中插 `runtime.GC()`/`Gosched`；校验结果一致 |
| net/art/table_test.go:623 | 同一批 1000 前缀做 10 次随机打乱插入，1 万次查询结果与插入顺序无关 | L03/L13 | 高 | 可复现随机源 + 两棵树并行构建；长循环；比较查询结果而非内部结构 |
| tstime/jitter_test.go:11 | 1..10s 随机抖动 500 次，结果始终落在 [min,max) | L19/L05 | 低 | `math/rand/v2` + 边界检查；种子可复现 |

## 推荐收录（15 条）

1. `l02_sharded_map` — `syncs/shardedmap_test.go:8`。分片 map 的 Set/Delete/Mutate 三态；P1，L02/L01/L13。
2. `l02_semaphore_ctx` — `syncs/syncs_test.go:145`。容量/取消/释放的并发原语；P1，L02/L04。
3. `l02_syncvalue_concurrent` — `types/lazy/sync_test.go:127`。10000 goroutine 单次初始化；P2，L02/L04/L06。
4. `l02_l03_shardvalue_distribution` — `syncs/shardvalue_test.go:16`。1000×10000 原子分片计数 + 长循环；P2，L02/L03/L13。
5. `l19_connstats_interval` — `net/connstats/stats_test.go:50`。周期 dump 定时器与 Shutdown；P1，L19/L02。
6. `l02_l03_connstats_concurrent` — `net/connstats/stats_test.go:82`。NumCPU goroutine ×1000 包 + 周期聚合；P2，L02/L03/L05。
7. `l19_memnet_pipe_deadline` — `net/memnet/pipe_test.go:33`。读写 deadline 与 Block 唤醒；P1，L19/L04。
8. `l02_memnet_pipe_limit` — `net/memnet/pipe_test.go:83`。容量 1 管道的写阻塞与逐字节读；P1，L02/L04/L13。
9. `l02_l19_derp_send_freeze` — `derp/derp_test.go:272`。慢消费者不阻塞其他目标；P2，L02/L04/L19。
10. `l19_derp_rate_limit` — `derp/derp_test.go:1522`。令牌桶发送限速 + 假时钟；P1，L19/L03。
11. `l04_l18_conn_memory_lifecycle` — `control/controlbase/conn_test.go:234`。1000 连接 goroutine/堆/关闭回收；P2，L04/L18/L19。
12. `l06_notify_reflect_fields` — `ipn/ipnlocal/bus_test.go:22`。reflect 遍历全部 struct 字段；P1，L06。
13. `l03_l19_bus_rate_limit` — `ipn/ipnlocal/bus_test.go:123`。last-value 合并 + flush 定时器；P2，L19/L03。
14. `l02_ipnserver_user_switch` — `ipn/ipnserver/server_test.go:191`。10×100 并发会话建立/断开；P2，L02/L04。
15. `l03_art_insert_probe` — `net/art/table_test.go:578`。1 万前缀/1 万查找长循环与参考实现比对；P2，L03/L13/L17。

## 不可移植清单

| 测试文件:行 | 依赖 | 不可移植原因 |
|---|---|---|
| portlist/portlist_test.go:49 | `/proc/net/*`、平台端口枚举、poll 间隔 | 仅 Linux/特定平台，依赖真实 socket 表与轮询 |
| portlist/netstat_test.go:55 | `netstat` 输出格式 | 解析结果随 OS/工具版本变化 |
| net/ktimeout/ktimeout_linux_test.go:16 | `setsockopt(TCP_USER_TIMEOUT)` | Linux socket 选项，需真实 TCP 连接 |
| net/netcheck/netcheck_test.go:112 | 真实 UDP + STUN + DERP 探测 | 依赖外部网络、NAT 与 DERP 服务器 |
| derp/xdp/xdp_linux_test.go:273 | eBPF/XDP、内核、root 权限 | 需要 cgo/内核加载器与真实网卡；环境强绑定 |
| wgengine/magicsock/magicsock_test.go:382 | WireGuard/disco 真实 UDP socket | 依赖网卡、端点发现与平台路由 |
| wgengine/router/router_linux_test.go:34 | netlink 路由/iptables | 需要 root 与 Linux 网络命名空间 |
| wgengine/netstack/netstack_test.go:39 | gVisor netstack + TUN | 需要 TUN 设备/root；`TestInjectInboundLeak` 还依赖完整协议栈 |
| net/dns/manager_linux_test.go:17 | systemd-resolved/resolvconf | 平台 DNS 管理，非纯 Go 逻辑 |
| net/dns/resolver/forwarder_test.go:555 | 真实 UDP/TCP DNS socket | 依赖 DNS 服务器与网络超时 |
| control/controlhttp/http_test.go:54 | Noise 握手 over HTTPS | 依赖真实 TLS/HTTP/2 与控制面密钥 |
| control/controlclient/direct_test.go:22 | Tailscale 控制面长轮询 | 依赖外部控制服务器与认证 |
| safesocket/basic_test.go:17 | Unix domain socket / Windows named pipe | 平台 IPC，路径与权限模型不同 |
| tsnet/tsnet_test.go:56 | 嵌入式 tailscaled + 真实网络 | 需要完整 daemon、TUN、控制面 |
| ssh/tailssh/tailssh_test.go:962 | OS 用户/PAM/sshd/root | 需要平台用户体系与提权；不可在单包用例复现 |
| prober/prober_test.go:189 | 真实 HTTP/TLS/DNS 探测 | 依赖外部端点与网络延迟 |
| hostinfo/hostinfo_test.go:12 | 平台文件/命令/内核接口 | 结果随 OS 与宿主环境变化 |
| ipn/ipnserver/server_test.go:191 | Windows 用户令牌/会话 | 测试本身依赖 Windows 身份模型；仅并发结构可借鉴（见推荐 14） |
