# stress —— 长跑复杂场景压测层

与 `cases/` 的 19 维语义用例并列的**长跑层**，不占 19 维配额。

## 约定

- 每个场景一个目录：`stress/<name>/main.go` + `scenario.md`。
- 仅标准库（例外须在 `requires` 标注）；单包 `package main`。
- 统一标记：成功最后一行 `STRESS PASS <name>`；失败 `FAIL: ...` 并退出 1。
- 参数（环境变量）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0` 表示固定轮数（快速自检） |
| `STRESS_CRASH_FILE` | runner 注入 | crash 文本落盘路径（`debug.SetCrashOutput`），core 不可用时用它 |
| `STRESS_WORKERS` | 场景默认 | 并发 worker/租户数 |
| `STRESS_HEARTBEAT` | 5 | 心跳检查间隔（秒），停滞 6 个周期判失败 |
| `UAF_SEED` | 1 | 随机路径种子，失败输出带出 |

- 场景内部要求：阶段化、持续不变量、心跳/watchdog、指标只记录不断言。

## 运行

```bash
SUITE=stress ./run.sh                       # 默认 60s × soak profile
SUITE=stress PROFILES=soak,soak-single REPEAT=1 ./run.sh
SUITE=stress STRESS_SECONDS=5 FILTER=s02 ./run.sh
SUITE=all ./run.sh                          # unit + stress
```

### 夜间跑批

```bash
STRESS_SECONDS=3600 PROFILES=soak ./stress/run_nightly.sh
FILTER=s04 STRESS_SECONDS=3600 ./stress/run_nightly.sh
```

日志在 `build/nightly-<stamp>/stress/`；失败时打印 crash/core 路径。

`STRESS PLAN / RESEARCH`：`docs/STRESS_PLAN.md`、`docs/STRESS_RESEARCH.md`。
