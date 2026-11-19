# 交接说明（下一阶段从这里继续）

## 当前状态

本仓库 **CoreGoVLLM-test** 是 CoreGoLLVM 的编译器稳定性测试仓，三层结构：

- **unit 层**（`cases/`）：950 个用例，19 个维度，每维 50 个；每个用例 `main.go` + `case.md`（场景、逐条期望、失败判读、参数、运行命令、真实来源 file:line）。
- **stress 层**（`stress/`）：长跑场景（60s～1h+），harness 统一（阶段化、不变量、心跳 watchdog、`STRESS_CRASH_FILE`、metrics 只记录）。s01–s06 已落地并全部通过官方 Go 1h 基线；n01–n12 已全部落地（共 18 个场景），固定轮数/60s×3 soak/`-race` 全绿；n 系列的 1h 官方基线待补。
- **调研资产**（`docs/research/`）：27 个三方库 partial clone（`research/repos/`，170MB，不入 git）+ 28+ 份场景挖掘/压测调研文档。

官方 Go 1.24.1 验证：950 unit × base/clobberfree/efence 全量；6 stress × soak × 1h；并发维度 `-race`。**尚未在 CoreGoLLVM 上实跑**。

## 关键文档

| 文件 | 用途 |
|---|---|
| `README.md` | 仓库总览、快速开始、runner 配置、判定 |
| `docs/RUNBOOK.md` | 内网 CoreGoLLVM 跑法（unit/stress/夜间/差分）、结果判读、失败回传 |
| `docs/DIMENSIONS.md` | 19 维定义、unit 用例契约、验收标准 |
| `docs/MATRIX.md` | unit 维度×用例矩阵（`tools/gen_matrix.py` 生成） |
| `docs/STRESS_PLAN.md` / `STRESS_RESEARCH.md` | stress 层方案与调研结论 |
| `docs/STRESS_MATRIX.md` | stress 场景矩阵（`tools/gen_stress_matrix.py` 生成） |
| `docs/research/stress_scenarios_survey.md` | 12 个新场景规划与业界依据 |
| `docs/SOURCES.md` | 41 个库 → 阶段/维度/状态 |
| `docs/PROGRESS.md` | 阶段进度与验证记录 |

## 内网起步

```bash
TOOLCHAIN=go ABI=gc PRIORITY=P0 PROFILES=base ./run.sh                  # unit 冒烟
TOOLCHAIN=go ABI=gc DIMS=L05,L13,L17,L18 \
  PROFILES=base,clobberfree,efence REPEAT=3 ./run.sh                    # UAF 核心
TOOLCHAIN=go ABI=gc STRESS_SECONDS=3600 PROFILES=soak \
  ./stress/run_nightly.sh                                               # stress 夜间
# 前端能力未知时：CAPS=reflect,goast,net ./run.sh 过滤不支持用例
```

## 已知限制

- CoreGoLLVM 实跑结果未回传；L01 泛型/L16 模板/L08 asm/L09 cgo/L15 多包依赖前端能力，用 `CAPS` 过滤；
- 官方 1h 基线目前覆盖 s01–s06；n 系列新场景需同样补基线；
- `tools/check_debuginfo.sh` 的 stack-map 符号检查在 Go 1.24 降级为 WARN（符号不再暴露），由 L17 运行级用例覆盖；
- `research/repos/` 被 gitignore，内网需另带 tarball。

## 下一批工作

1. n01–n12 已完成；补 n 系列 1h 官方基线（12 × 1h，可分批）。
2. 内网 S2 夜间：6+12 场景 × soak × 1h × 3 晚；收集 crash/core 并与官方基线对照。
3. 差分：`STRESS_SECONDS=0` + 同 seed/轮数，官方 vs CoreGoLLVM 比校验和。
4. CI：P0 unit 冒烟（分钟级）+ nightly stress 60s + weekly 1–8h。
