# 长时间复杂场景压测层（stress）方案

## 1. 目标与非目标

**目标**：在复杂、长期运行的负载下暴露 CoreGoLLVM 的偶发 UAF/栈映射/调度问题；把"短小确定性用例"升级为"小时级真实形态压测"。

**非目标**：不替代 `cases/` 的 19 维语义回归；不重复库级差分（已由其他人负责）；不追求性能基准。

## 2. 与 19 维 cases 的关系：两层，互不污染

| 层 | 目录 | 形态 | 判定 | 时长 | 矩阵 |
|---|---|---|---|---|---|
| 语义回归 | `cases/L01…L19/<case>/` | 单文件 main.go，确定性 | 退出码 0 + `ALL PASS <case>` | 0.1–2s | `docs/MATRIX.md`，每维 50 |
| 压测层 | `stress/s01…s10/<scenario>/` | 多阶段长跑程序，自校验+心跳 | 退出码 0 + `STRESS PASS <name>` + 无 FAIL/panic | 60s（CI）～1–8h（夜间） | `docs/STRESS_MATRIX.md`，按场景×覆盖维度 |

- 压测场景**不计入** 19 维的 50 个配额；每个场景在 `scenario.md` 中用 `covers: [L02,L05,L17,L18]` 声明交叉维度。
- `cases/` 保持现状不动；runner 增加 `SUITE` 选择，互不影响。
- 共享约定：单包 `package main`、仅标准库（asm/cgo 场景显式 `requires`）、失败打印 `FAIL:` 并退出 1。

## 3. 目录结构

```
stress/
├── README.md
├── s01_service_soak/
│   ├── main.go
│   └── scenario.md             # frontmatter + 场景/不变量/参数/失败判读
├── s02_heap_cycle_soak/
├── s03_stack_churn_soak/
├── s04_preempt_storm_soak/
├── s05_generated_code_soak/
├── s06_queue_pipeline_soak/
├── s07_unsafe_io_soak/
├── s08_cache_wb_soak/
└── ...
```

初版 6 个场景，稳定后扩到 10 个。

## 4. Runner 设计（已采用：单 runner + `SUITE`）

在现有 `run.sh` 增加：

| 变量 | 默认 | 说明 |
|---|---|---|
| `SUITE` | `unit` | `unit` / `stress` / `all` |
| `STRESS_SECONDS` | `60` | 目标运行时长（场景内部按此循环，支持 `0`=固定轮数） |
| `STRESS_HEARTBEAT` | `5` | 心跳间隔（秒），runner 监控停滞 |
| `STRESS_WORKERS` | 场景默认 | 并发租户/goroutine 数 |
| `STRESS_PROFILES` | `soak` | `soak`=GOGC=1+clobberfree+GOMEMLIMIT+GOMAXPROCS=2；`soak-single`=GOMAXPROCS=1；`soak-noasync`=asyncpreemptoff=1 |
| `TIMEOUT` | `STRESS_SECONDS+120` | 超时 |

压测判定（与 unit 不同）：
1. 退出码 0；
2. stdout 含 `STRESS PASS <scenario>`；
3. 无 `FAIL:` / `panic:` / `fatal error`；
4. 心跳未停滞超过 `STRESS_HEARTBEAT*3`；
5. 结束时打印 `metrics: checks=… heap=… goroutines=…`（只记录，不做绝对阈值断言）。

产物：`build/stress/<scenario>.<profile>.<seed>.log`；崩溃时保留 core（`GOTRACEBACK=crash`，runner 打印 core 路径）。

## 5. 场景规范（scenario.md 契约）

frontmatter：

```yaml
---
id: s01_service_soak
covers: [L02, L05, L17, L18, L19]
requires: []
default_seconds: 60
max_workers: 64
---
```

程序内部要求：
- **阶段化**：warmup → steady（分配/释放/并发）→ shrink（`debug.FreeOSMemory`）→ regrow → 收尾校验；
- **持续不变量**：每条消息/对象/序列号校验，发现损坏立即 `FAIL` 并退出；
- **GC 压力**：独立 churn goroutine 持续 `runtime.GC()` + 垃圾分配；周期性改 `GOGC`（可选）；
- **心跳**：主循环每 N 轮写 `atomic` 心跳，watchdog goroutine 检测停滞（死锁/GC 饥饿）；
- **参数化**：`STRESS_SECONDS`、`STRESS_WORKERS`、`UAF_SEED`、`GOMEMLIMIT` 由环境注入；
- **可复现**：固定种子可重跑；失败输出场景名、阶段、种子、worker、期望/实际值。

## 6. 初版场景（6 个）

| 场景 | 形态 | 主要覆盖 | 关键不变量 |
|---|---|---|---|
| s01_service_soak | 进程内 HTTP/JSON 或 net.Pipe 服务，goroutine-per-request，缓存/map/slice/interface，请求持续整个时长 | L02/L05/L06/L17/L18/L19 | 响应校验和、序列号、缓存内容 |
| s02_heap_cycle_soak | 大对象/指针密集数组环形保留 + 周期性 FreeOSMemory + 重新增长 | L05/L18 | 存活对象内容、fresh 分配全零 |
| s03_stack_churn_soak | 深递归/大栈帧/闭包递归/panic-recover 循环，GC 落在增长与展开中 | L04/L17 | 每层指针值、展开后 payload |
| s04_preempt_storm_soak | 多 goroutine 紧循环持针 + 并发 GC，GOMAXPROCS 1/2/4 切换 | L03/L02 | 校验和、指针目标 |
| s05_generated_code_soak | 生成风格大 struct/方法集/switch 表，持续 marshal/unmarshal | L10/L06/L11 | 编解码 roundtrip |
| s06_queue_pipeline_soak | 多级 channel 流水线、背压、取消、timer、map 缓存 | L02/L19/L13 | 消息计数/顺序/内容 |

稳定后追加：s07 unsafe/零拷贝 IO、s08 老→新缓存写屏障、s09 finalizer/pool 队列、s10 混合多租户（S1+S2+S6）。

## 7. 验证与验收

- **单场景**：默认 60s × 3 个 profile 全过；`UAF_SEED=1..10` 各 1 次；失败可复现并给出种子。
- **夜间**：6 场景 × 3 profile × 1h（`STRESS_SECONDS=3600`），零 FAIL/panic/停滞。
- **周级**：10 场景 × 8h，GOMEMLIMIT 限制，记录峰值 heap/goroutine。
- **差分（后续）**：同一场景/种子分别用官方 Go 1.24.1 与 CoreGoLLVM 构建，比较最终校验和与存活对象数（不比较性能）。
- **CI 分层**：P0 unit 冒烟（分钟级）→ nightly stress 60s → weekly stress 1–8h。

## 8. 实施阶段

| 阶段 | 交付 | 退出条件 |
|---|---|---|
| S0 | runner `SUITE=stress` + scenario 模板 + s02/s03 两个试点 | 官方 Go 上 60s × 3 profile 全绿 |
| S1 | 补齐 s01/s04/s05/s06 | 完成：6 场景 60s × 3 profile 全绿；`STRESS_MATRIX.md` 已生成 |
| S2 | 夜间 1h 跑批 + 崩溃 core 收集 + watchdog 告警 | 连续 3 晚无 FAIL |
| S3 | 差分压测 + 扩到 10 场景 | 官方 vs CoreGoLLVM 校验和一致 |

## 9. 风险与规避

- **墙钟/内存断言易抖动**：只做不变量与结构断言；metrics 仅记录。
- **长跑掩盖死锁**：watchdog + 心跳；runner 超时独立于场景内部时长。
- **环境差异（cgroup/GOMEMLIMIT）**：profile 参数化，不写死。
- **场景本身有竞态**：并发场景需通过 `-race`（在官方 Go 上）后再纳入夜间。
- **CI 成本**：默认 60s，长跑只在夜间/周级。

## 10. 已拍板 / 状态

- 目录：`stress/`（与 `cases/` 并列，不占 19 维配额）。
- runner：单入口 `SUITE=unit|stress|all`，stress 使用 `soak/soak-single/soak-noasync` profile。
- S0 已落地：`stress/s02_heap_cycle_soak`、`stress/s03_stack_churn_soak`、`stress/README.md`。
- 下一步：S0 60s × 3 profile 验收通过后，补 s01/s04/s05/s06。
