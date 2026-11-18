# 进度日志

## Phase 0 — 基建（完成）

- 目录按 19 维重组：`cases/L01…L19`；旧用例已删除重建。
- `run.sh` v2：递归发现、`DIMS`/`PRIORITY`/`FILTER`/`CAPS` 过滤、`UAF_SEED` 注入、oracle、golden、多 profile。
- `docs/DIMENSIONS.md`（维度定义/用例契约/验收）、`docs/SOURCES.md`（41 库）、`tools/gen_matrix.py`、`tools/fetch_lib.sh`、`templates/case/`。
- 官方对照工具链：`GOTOOLCHAIN=go1.24.1`。

## Phase 1 — L05/L13/L17/L18（完成）

- 40 用例（每维 10），来源：badger、pgx、go-redis、goccy/go-json、klauspost/compress。
- 验证：40 × 5 profile × 2 seed 全绿；10-seed 长压测（base/k8s/clobberfree，1200 次）全绿；P0 23 例冒烟 5.5s。

## Phase 2 — L02/L03/L04/L06/L07（完成）

- 50 用例（每维 10），来源新增：fasthttp、gin、echo、fiber、grpc-go、pion/webrtc、tailscale、etcd、caddy、frp、gorm；沿用 nats-server、quic-go 等调研。
- 累计：9 个维度、90 个用例。
- 验证：
  - 90 × 5 profile × 2 seed 全绿；
  - 并发三维（L02/L03/L04）30 用例 `-race` × 2 seed 全绿；
  - 10-seed 长压测（base/k8s/clobberfree）进行中/结果见下。

### Phase 2 soak 结果

- 90 用例 × 3 profile（base/k8s/clobberfree）× 10 seed 全绿（`/tmp/gcuaf_phase2_soak.log`）。

## Phase 3 — L01/L10/L11/L12/L16（完成）

- 50 用例（每维 10），来源：ent、sqlc、protobuf-go、kitex、hugo、yaegi、gitea、golangci-lint（另有 pgx/go-redis/grpc-go 等既有调研）。
- L19（新增维度）同步补齐 10 个 netpoll/定时器/信号用例。
- 验证：新增 70 用例（Phase 3 + Phase 4 基础 + L19）× 5 profile × 2 seed 全绿；全量 160 × 5 profile 回归全绿（`/tmp/gcuaf_all160.log`）。

## Phase 4 — L08/L09/L14/L15（基础完成，待扩容）

- 已落地 10 个用例：L08 asm×2（ABI0 整数/指针、FNV 汇编循环）、L09 cgo×2（纯 C 调用、C.malloc+Go 切片指针）、L14×3（traceback、内联帧、goroutine dump）、L15×3（GOOS/GOARCH/自定义 tags）。
- 工具：`tools/check_debuginfo.sh`（DWARF subprogram/行号检查；stack-map 符号在 Go 1.24 工具链不再暴露，降级为提示，由 L17 运行级用例覆盖）。
- runner 增强：`BUILD_MODE=module`（目录构建/build tag/.s）、`BUILD_ONLY=1`、`.s` 文件、`BUILD_FLAGS` 透传。
- 验证：L08/L09/L14/L15 × base/clobberfree/efence 全绿。

## stress 长跑层 — S0（完成）

- runner 增加 `SUITE=unit|stress|all`、`soak/soak-single/soak-noasync` profile、stress 标记判定、独立 cwd（core 落在 build）。
- 试点场景：`s02_heap_cycle_soak`（堆循环/FreeOSMemory/fresh 零化）、`s03_stack_churn_soak`（深递归/panic/大帧/栈收缩）。
- 验证：固定轮数 + 5s × 3 soak profile + `-race` 通过；60s × 3 profile 全绿（`/tmp/gcuaf_stress_s0.log`）。
- 调研：`docs/STRESS_RESEARCH.md`、`docs/research/stress_{go_patterns,corpus,libraries}.md`。

## stress 长跑层 — S1（完成）

- 场景：s01 服务、s02 堆循环、s03 栈 churn、s04 抢占风暴、s05 生成代码（9590 行生成物）、s06 流水线。
- 验证：6 场景 × 3 soak profile × 60s 全绿（`/tmp/gcuaf_stress_s1.log`）；固定轮数 + 各场景自测通过；全量 6 场景 `-race` 检查全绿（`/tmp/gcuaf_stress_s1_race.log`）。
- 矩阵：`docs/STRESS_MATRIX.md`（`tools/gen_stress_matrix.py` 生成）。

## stress 长跑层 — S2 工具（完成）

- 场景支持 `STRESS_CRASH_FILE`（`debug.SetCrashOutput`），runner 注入并收集 crash 文本；失败时打印 crash/core 路径。
- `stress/run_nightly.sh`：默认 6 场景 × soak × 1h，独立 `build/nightly-<stamp>/`，失败归档。
- RUNBOOK 增加 stress 长跑/差分/内网夜间跑法。
- 官方 Go 1h 基线进行中：s01 服务、s04 抢占风暴（`/tmp/stress_s01_1h.log`、`/tmp/stress_s04_1h.log`）。

## Phase 5 — 规模化（进行中）

- 第一批（完成）：L05/L17/L18 各追加 10 个（→20/维），L08/L09/L14/L15 补齐到 10 个；累计 220 用例。
  验证：变更的 7 个维度 100 用例 × 5 profile × 2 seed 全绿（`/tmp/gcuaf_wave1.log`）。
- 第二批（完成）：L02/L03/L04/L06/L07/L13 各追加 10 个（→20/维）；累计 280 用例。
  验证：6 个维度 120 用例 × 5 profile × 2 seed 全绿（`/tmp/gcuaf_wave2.log`）。
- 第三批（完成）：L01/L10/L11/L12/L16/L19 各追加 10 个（→20/维）；累计 340 用例，19 维全部 ≥10（15 维 =20，L08/L09/L14/L15=10）。
  验证：6 个维度 120 用例 × 5 profile × 2 seed 全绿（`/tmp/gcuaf_wave3.log`）；全量 340 × 5 profile 回归全绿（`/tmp/gcuaf_all340.log`）。
- 第四批（完成）：L08/L09/L14/L15 各追加 10 个（→20/维）；累计 380 用例，19 维全部 =20。
  验证：L08/L09/L14/L15 共 80 用例 × 5 profile × 2 seed 全绿（`/tmp/gcuaf_final80.log`）；最终 380 × base/clobberfree/efence 回归全绿（`/tmp/gcuaf_final380.log`）。
- 并发维度 `-race`：L02/L03/L04 全绿；L19 首轮发现 `l19_afterfunc_reset` 测试自身竞态（timer 变量未同步），已用 mutex 修复，L19 `-race` 20/20 全绿。
- 第五批（完成）：19 维各追加 10 个（→30/维）；累计 570 用例。
  验证：570 × base/clobberfree/efence 全绿；并发维度 120 用例 `-race` 全绿；核心 210 用例 stress/k8s 全绿。
- 第六批（完成）：19 维各追加 10 个（→40/维）；累计 760 用例。
  验证：760 × base/clobberfree/efence 回归进行中。
- 第七批（完成）：19 维各追加 10 个（→50/维）；累计 950 用例，目标规模达成。
  验证：950 × base/clobberfree/efence 全绿（`/tmp/gcuaf_all950_final.log`）；L14 边界用例修复后 50/50 × 5 profile 全绿。
  并发维度 200 用例 `-race` 全绿；核心 350 用例 × stress/k8s 全绿。
  950 目标规模达成；长压测/差分/CI 待接入。
- 后续：长压测/差分/CI 接入。

