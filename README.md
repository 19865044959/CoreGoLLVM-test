# CoreGoLLVM-test — Go 编译器稳定性测试仓

面向 **CoreGoLLVM**（LLVM 后端的 Go 编译器）的运行级稳定性验证仓。三层结构：

1. **unit 语义回归**（`cases/`）：950 个独立、自校验、输出确定的用例，按 19 个 Go 语言/运行时维度组织（GC/栈映射/UAF、并发/抢占/调度、泛型/反射/unsafe、模板/代码生成、汇编/cgo/DWARF/构建标签…）。
2. **stress 长跑压测**（`stress/`）：复杂负载下的小时级场景（服务、堆循环、栈、抢占风暴、生成代码、流水线、timer/netpoll、map、finalizer、写屏障、trace/pprof、exec/信号、cgo、解析/类型检查、TLS/连接、LSM、MVCC/watch、fuzz 解码），持续不变量 + 心跳 watchdog。
3. **调研资产**（`docs/research/`）：27 个三方库测试套的场景挖掘、官方压测模式、业界做法，作为两层用例的来源与溯源。

用例场景从 41 个主流三方库与 Go 官方测试调研提炼后用 **stdlib 重写**，不依赖三方库，可在内网离线编译运行。所有用例已在官方 Go 1.24.1 上全量验证。

## 快速开始

```bash
# unit：官方 Go 对照（建议 1.24.1）
export GOTOOLCHAIN=go1.24.1
TOOLCHAIN=stock PROFILES=base ./run.sh              # 全量（默认 5 profile × REPEAT=3）
PRIORITY=P0 ./run.sh                                # P0 冒烟（分钟级）
DIMS=L05,L17 ./run.sh                               # 按维度
./run.sh l05_pool                                   # 按名字
./run.sh list

# stress：长跑压测
SUITE=stress STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=10 PROFILES=base ./run.sh   # 固定轮数自检
STRESS_SECONDS=3600 PROFILES=soak ./stress/run_nightly.sh                      # 夜间 1h 跑批
SUITE=all ./run.sh                                 # 两层都跑

# CoreGoLLVM（内网）
TOOLCHAIN=go ABI=gc ./run.sh                        # go build -go-abi=gc
TOOLCHAIN=goc GOC=/path/to/llvm-goc ABI=gc ./run.sh # llvm-goc -fgo-abi=gc

# 与官方 Go 逐行 diff（unit）
TOOLCHAIN=go ORACLE=1 ORACLE_GO=go ./run.sh
```

## 目录

```
CoreGoVLLM-test/
├── run.sh                      # 统一 runner：unit/stress 发现、构建、运行、判定
├── cases/L01…L19/<case>/       # unit 用例：main.go + case.md（950 个）
├── stress/<scenario>/          # 长跑场景：main.go + scenario.md
│   └── run_nightly.sh          # 夜间跑批（默认 1h × soak）
├── docs/
│   ├── DIMENSIONS.md           # 19 维定义、用例契约、验收标准
│   ├── MATRIX.md               # unit 维度×用例矩阵（自动生成）
│   ├── STRESS_PLAN.md          # stress 层方案
│   ├── STRESS_RESEARCH.md      # stress 调研结论（代码量/复用）
│   ├── STRESS_MATRIX.md        # stress 场景矩阵（自动生成）
│   ├── SOURCES.md              # 41 个三方库 → 阶段/维度/状态
│   ├── RUNBOOK.md              # 内网运行手册（判定/判读/回传）
│   ├── PROGRESS.md             # 阶段进度与验证记录
│   ├── HANDOFF.md              # 交接说明
│   └── research/<lib>.md       # 每库场景挖掘 + stress 专项调研
├── tools/
│   ├── gen_matrix.py           # 生成 MATRIX.md
│   ├── gen_stress_matrix.py    # 生成 STRESS_MATRIX.md
│   ├── gen_stress_code.py      # s05 生成代码（9590 行）
│   ├── fetch_lib.sh            # partial clone 三方库测试文件
│   └── check_debuginfo.sh      # DWARF/栈映射符号检查
├── templates/case/             # unit 用例模板
└── research/repos/             # 三方库测试代码（不入 git）
```

## runner 配置（节选）

| 变量 | 默认 | 说明 |
|---|---|---|
| `SUITE` | `unit` | `unit` / `stress` / `all` |
| `TOOLCHAIN` | `go` | `go`（带 `-go-abi` 的驱动）/ `goc`（llvm-goc）/ `stock`（标准 go） |
| `DIMS` / `PRIORITY` / `FILTER` / `CAPS` | 空 | 维度、优先级、名字、能力过滤 |
| `PROFILES` | unit: `base,clobberfree,efence,stress,k8s`；stress: `soak` | 运行环境矩阵；另有 `single`/`noasync`/`soak-single`/`soak-noasync` |
| `REPEAT` / `SEED_BASE` | 3 / 1 | 重复次数 = `UAF_SEED` 注入 |
| `STRESS_SECONDS` 等 | 60 等 | stress 场景时长/worker/心跳 |
| `ORACLE` / `BUILD_ONLY` / `BUILD_FLAGS` | 0 / 0 / 空 | 官方对照、只编译、透传编译参数 |

## 判定与判读

- unit：退出码 0 + 最后一行 `ALL PASS <case>`；
- stress：退出码 0 + 最后一行 `STRESS PASS <name>` + 心跳不停滞（watchdog）+ 无 FAIL/panic；
- 失败产物：`build/` 下日志；stress 另有 `*.crash.txt`（`debug.SetCrashOutput`）与 core（若环境允许）；
- 常见信号：SIGSEGV/`fatal error: found bad pointer` = 栈映射/写屏障；`FAIL:` 垃圾值（clobberfree）= UAF；退出码 124 = 超时/死锁。

详见 `docs/RUNBOOK.md`。

## 用例契约

- unit：单目录 `main.go` + `case.md`（场景/逐条期望/失败判读/参数/运行/来源 file:line）；仅 stdlib；输出确定；`requires` 能力标签（`generics/asm/cgo/pkgbuild/goast/reflect/net`…）供 `CAPS` 过滤；多文件/构建标签用 `BUILD_MODE=module`。
- stress：单目录 `main.go` + `scenario.md`；harness 统一（阶段化、不变量、心跳 watchdog、`STRESS_CRASH_FILE`、metrics 只记录）；不占 unit 维度配额。

详见 `docs/DIMENSIONS.md`、`stress/README.md`。

## 规模与状态

| 层 | 规模 | 状态 |
|---|---|---|
| unit cases | 950（19 维 × 50；P0 169 / P1 536 / P2 245） | 完成 |
| stress scenarios | 18（s01–s06 + n01–n12，见 survey） | s01–s06 含官方 Go 1h 基线；n 系列待补 |
| 三方库调研 | 27 库 mined/adapted，14 planned | 持续 |

官方 Go 1.24.1 验证：950 unit 全 profile；18 stress × 60s × 3 soak 全绿 + `-race`；s01–s06 另有 1h 基线。

## 三方库与许可

- 调研用 partial clone（`tools/fetch_lib.sh`，仅测试文件/testdata，不入 git）；
- 用例只借鉴场景形态，不逐行拷贝；stdlib 重写；
- testdata 复用遵循 `docs/research/stress_corpus.md`（GPL/隐私语料禁用，小样本入库、大件运行时读取、合成兜底）。

网络（仅调研）：WSL 代理见 `/mnt/d/note/为wsl随时配置VPN.md`。
