# 交接说明（下一阶段从这里继续）

## 当前状态

- **950 个用例，19 个维度，每维 50 个**（`cases/L01…L19`）。
- 每个用例：`main.go` + `case.md`（场景、逐条期望、失败判读、参数、运行命令、真实来源 file:line）。
- 来源：27 个三方库测试套的 partial clone（`research/repos/`，170MB，不入 git），调研文档 28 份（`docs/research/`）。
- 官方 Go 1.24.1 验证：950 × base/clobberfree/efence 全量回归；分批 5 profile ×2 seed 验证；并发维度 L02/L03/L04/L19 通过 `-race`。

## 关键文档

| 文件 | 用途 |
|---|---|
| `docs/RUNBOOK.md` | 内网 CoreGoLLVM 的推荐跑法、CAPS 过滤、结果判读 |
| `docs/DIMENSIONS.md` | 19 维定义、case.md 契约、验收标准 |
| `docs/MATRIX.md` | 维度×用例矩阵（`tools/gen_matrix.py` 自动生成） |
| `docs/SOURCES.md` | 41 个库 → 阶段/维度/状态（27 adapted，14 planned） |
| `docs/PROGRESS.md` | 各阶段与每批验证记录 |
| `docs/research/*.md` | 每个库的场景表与不可移植清单 |

## 内网起步

```bash
cd CoreGoVLLM-test
TOOLCHAIN=go ABI=gc PRIORITY=P0 PROFILES=base ./run.sh          # 冒烟
TOOLCHAIN=go ABI=gc DIMS=L05,L13,L17,L18 \
  PROFILES=base,clobberfree,efence REPEAT=3 ./run.sh            # UAF 核心
TOOLCHAIN=go ABI=gc CAPS=generics,reflect,templates,goast,net \
  PROFILES=base,clobberfree,k8s ./run.sh                        # 按能力过滤全量
```

## 已知限制

- 用例只在官方 Go 1.24.1 上验证过；**尚未在 CoreGoLLVM 上实跑**。
- L01 泛型、L16 模板/goast/reflect、L08 asm、L09 cgo、L15 多包构建依赖前端/平台能力，均带 `requires`，用 `CAPS` 过滤。
- `tools/check_debuginfo.sh` 的 stack-map 符号检查在 Go 1.24 工具链降级为 WARN（符号不再暴露），L17 运行级用例覆盖该风险。
- `build/` 与 `research/repos/` 已在 `.gitignore`；`build/` 曾存在于 git 历史，建议 `git rm -r --cached build`。

## 下一批工作

1. 夜间长压测：`REPEAT=20 PROFILES=base,k8s,clobberfree`，记录 flaky 用例。
2. 与官方 Go 1.24.1 差分：`ORACLE=1` 覆盖 P0 集合。
3. CI 接入：P0 冒烟 + 每日 P1 + 周级 P2。
