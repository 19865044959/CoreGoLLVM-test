# gc-uaf-tests v2 — Go 编译器稳定性测试套（19 维度）

面向 CoreGoLLVM 的**运行级**稳定性测试仓：每个用例是一个可独立编译的 `main.go`，自校验、输出确定，覆盖 GC/栈映射/UAF、并发/抢占/调度、泛型/反射/unsafe、模板/代码生成、汇编/cgo/DWARF 等 19 个语言特性维度。用例场景从 41 个主流三方库的测试套中调研、提炼后用 stdlib 重写，**不依赖三方库**，可在内网离线编译运行。

## 当前状态

- Phase 0（骨架/规范/runner/矩阵生成）已完成；
- Phase 1（L05/L13/L17/L18，UAF 核心）已完成：4 维 × 10 用例，来源覆盖 badger、pgx、go-redis、goccy/go-json、klauspost/compress（场景表见 `docs/research/`）；
- Phase 2（L02/L03/L04/L06/L07）已完成：累计 9 个维度、90 个用例；
- Phase 3（L01/L10/L11/L12/L16）已完成：累计 19 个维度、160 个用例（含 L19 的 10 个）；
- Phase 4（L08/L09/L14/L15）已完成基础覆盖（各 10 用例，asm/cgo/DWARF/构建标签）；
- Phase 5 进行中：19 维共 380 用例（每维 20），目标每维 50。

## 目录

```
CoreGoVLLM-test/
├── run.sh                  # 运行器：递归发现、维度/优先级过滤、seed、oracle
├── cases/<Lxx_维度>/<用例>/  # main.go + case.md
├── docs/ (DIMENSIONS/SOURCES/MATRIX/RUNBOOK/PROGRESS)
│   ├── DIMENSIONS.md       # 19 维定义、用例规范、验收标准
│   ├── SOURCES.md          # 41 个三方库 → 阶段/维度/状态
│   ├── MATRIX.md           # 维度×用例矩阵（tools/gen_matrix.py 生成）
│   └── research/<lib>.md   # 每个库的场景挖掘结果
├── tools/
│   ├── gen_matrix.py       # 生成 MATRIX.md
│   └── fetch_lib.sh        # partial clone 三方库测试文件
├── templates/case/         # main.go / case.md 模板
└── research/repos/         # 三方库测试代码（不入 git）
```

## 快速开始

```bash
# 官方 Go 对照（建议 1.24.1；本机可用 GOTOOLCHAIN 自动拉取）
export GOTOOLCHAIN=go1.24.1
TOOLCHAIN=stock PROFILES=base ./run.sh

# CoreGoLLVM（内网）
TOOLCHAIN=go ABI=gc ./run.sh
TOOLCHAIN=goc GOC=/path/to/llvm-goc ABI=gc ./run.sh

# 维度 / 优先级 / 用例过滤
DIMS=L05,L17 ./run.sh
PRIORITY=P0 ./run.sh
./run.sh l05_pool
./run.sh list

# 官方 Go 对照跑（逐行 diff 输出）
TOOLCHAIN=go ORACLE=1 ORACLE_GO=go ./run.sh
```

默认 profile：`base,clobberfree,efence,stress,k8s` × `REPEAT=3`（每次注入不同 `UAF_SEED`）。

## runner 配置

| 变量 | 默认 | 说明 |
|---|---|---|
| `TOOLCHAIN` | `go` | `go`=带 `-go-abi` 的驱动；`goc`=llvm-goc；`stock`=标准 go |
| `GO`/`GOC`/`ORACLE_GO` | `go`/`llvm-goc`/`go` | 工具链路径 |
| `ABI` | `gc` | `gc` / `gollvm` |
| `DIMS` | 空 | 维度过滤，逗号分隔，支持前缀（`L05`） |
| `PRIORITY` | 空 | `P0,P1,P2` 过滤（读 case.md） |
| `FILTER` | 空 | 用例名子串过滤（也可 `./run.sh <substr>`） |
| `CAPS` | 空 | 能力白名单；为空不过滤，非空时跳过 `requires` 不全在其中的用例 |
| `PROFILES` | `base,clobberfree,efence,stress,k8s` | `k8s`=GOMAXPROCS=2+GOMEMLIMIT；另有 `single`/`noasync` 对照 |
| `REPEAT` | `3` | 每个 profile 重复次数，同时作为 `UAF_SEED` |
| `SEED_BASE` | `1` | 种子起点 |
| `TIMEOUT` | `120` | 单次超时（秒） |
| `ORACLE` | `0` | `1` 时用标准 go 编译并逐行 diff |
| `BUILD_ONLY` | `0` | `1` 时只编译不运行（asm/cgo/交叉编译用例） |
| `BUILD_FLAGS` | 空 | 透传编译参数（如 `-tags=featureA`、`-race`） |

## 用例约定（必须遵守）

- 用例目录名 = `ALL PASS` 后的名字；失败打印 `FAIL: ...` 并退出 1。
- 只允许 stdlib；不 import 三方库；不依赖 CGO/汇编（除非在 L08/L09 且标注 `requires`）。
- 输出确定，不打印地址/时间/随机数/map 顺序。
- `case.md` 必须写清场景、逐条期望行为、失败判读、参数、运行命令；`sources` 标注调研来源。
- 可选文件：`.skip-envs`、`.env`、`.expected`（golden 输出，含 stderr）。
- 需要特殊编译的用例：`BUILD_MODE`（内容 `module`，按目录构建，支持 build tag/多文件/`.s`）。
- `requires` 能力标签：`generics`（L01）、`templates`/`goast`/`reflect`（L16 等）、`asm`+`amd64`（L08）、`cgo`（L09）、`pkgbuild`（L15）。内网用 `CAPS=...` 过滤不支持的用例。

详见 `docs/DIMENSIONS.md`、`templates/case/`。

## 阶段计划

| 阶段 | 维度 | 目标 | 状态 |
|---|---|---|---|
| P0 | 基建/迁移/规范/runner | 可运行骨架、矩阵生成 | 完成 |
| P1 | L05/L13/L17/L18 | 各 +10（UAF 核心，共 40） | 完成 |
| P2 | L02/L03/L04/L06/L07 | 各 +10（共 50） | 完成 |
| P3 | L01/L10/L11/L12/L16 | 各 +10（共 50） | 完成 |
| P4 | L08/L09/L14/L15 | 各 10（asm/cgo/DWARF/build tags） | 完成 |
| P5 | 全部 | 380/950（每维 20，分批向 50 推进） | 进行中 |

## 三方库调研

- `docs/SOURCES.md`：41 个库的版本、阶段、维度、状态。
- `tools/fetch_lib.sh`：partial clone（只取测试文件/testdata），研究用，不入 git。
- 研究产物为 `docs/research/<lib>.md` 场景表；用例场景从表中挑选，重写为 stdlib 版本并在 `case.md` 标注来源。
- 不逐行拷贝三方库代码；如需 vendor 原测试代码，先做许可证审查。

网络（仅调研需要）：WSL 可按 `/mnt/d/note/为wsl随时配置VPN.md` 设置 `http_proxy=http://$(nameserver):7890`。
