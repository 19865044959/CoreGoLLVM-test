# 内网运行手册（CoreGoLLVM）

## 0. 前置

- 编译器：`TOOLCHAIN=go`（带 `-go-abi` 的 go 驱动）或 `TOOLCHAIN=goc`（llvm-goc）。
- 官方对照机：Go 1.24.1（`GOTOOLCHAIN=go1.24.1`），用于 `ORACLE=1` 差分与排除用例自身问题。
- 用例目录：`cases/<维度>/<用例>/main.go`，期望行为写在同目录 `case.md`。

## 1. 推荐验证顺序

```bash
cd CoreGoVLLM-test

# 1) P0 冒烟（几分钟内）
TOOLCHAIN=go ABI=gc PRIORITY=P0 PROFILES=base REPEAT=1 ./run.sh

# 2) UAF 核心：clobberfree + efence
TOOLCHAIN=go ABI=gc DIMS=L05,L13,L17,L18 \
  PROFILES=base,clobberfree,efence REPEAT=3 ./run.sh

# 3) 并发/抢占/调度：k8s profile（GOMAXPROCS=2 + GOMEMLIMIT）
TOOLCHAIN=go ABI=gc DIMS=L02,L03,L04,L19 \
  PROFILES=base,clobberfree,k8s REPEAT=5 ./run.sh

# 4) 全量（默认 5 profile × 3 seed）
TOOLCHAIN=go ABI=gc ./run.sh
```

## 2. 特殊维度与 CAPS

部分用例依赖前端/平台能力，内网按支持情况过滤；`CAPS` 非空时，`requires` 不被完全包含的用例会被跳过：

| 维度 | requires | 说明 |
|---|---|---|
| L01_generics | `generics` | 前端是否支持 Go 1.18+ 泛型 |
| L08_asm_simd | `asm, amd64` | 手写 Go 汇编 + amd64 |
| L09_cgo | `cgo` | cgo + C 工具链 |
| L15_build_tags | `pkgbuild` | 按目录/module 构建 |
| L16_template_interp | `templates` / `goast` / `reflect` | 模板、go/parser+types、reflect |
| L19_netpoll_timer_signal | `net` / `signals,unix` | 回环网络、Unix 信号 |

示例：

```bash
# 假设前端支持泛型/反射/模板，不支持 asm/cgo
CAPS=generics,reflect,templates,goast,net ./run.sh

# 只跑某维度
DIMS=L17 ./run.sh
```

构建失败或运行失败都会记为 `[BUILD-FAIL]` / `[FAIL]`，日志在 `build/`。

## 3. 特殊模式

```bash
# 只构建不运行（asm/cgo/交叉编译）
BUILD_ONLY=1 DIMS=L08,L09 ./run.sh

# 只构建某架构（示例：arm64）
GOARCH=arm64 BUILD_ONLY=1 DIMS=L15 ./run.sh

# 与官方 Go 1.24.1 逐行对照
GOTOOLCHAIN=go1.24.1 TOOLCHAIN=stock ORACLE=1 ORACLE_GO=go ./run.sh

# 调试信息（DWARF）检查脚本（L14）
GOTOOLCHAIN=go1.24.1 tools/check_debuginfo.sh \
  cases/L14_debug_info/l14_traceback_frames /tmp/dbgbin
```

## 4. 结果判读

| 现象 | 含义 |
|---|---|
| `[BUILD-FAIL]` | 前端/工具链不支持该语法或平台（先看是否应被 CAPS 过滤） |
| 退出码 139 / SIGSEGV / `fatal error: found bad pointer` | 栈映射漏标/写屏障缺失导致对象被回收 |
| `FAIL:` + clobberfree 垃圾值 | 确定性的 UAF/覆盖 |
| `finalizer ran while object reachable` | 对象仍可达却被判死（stackmap 漏标） |
| 退出码 124 | 超时（死锁/GC 饥饿） |
| `missing pass marker` | 程序提前退出或输出被破坏 |

## 5. 失败提交信息

请附：

1. 用例路径（`cases/<维度>/<用例>`）、profile、`UAF_SEED`；
2. `build/<维度>__<用例>.<profile>.<seed>.log`；
3. 是否在官方 Go 1.24.1 下可复现（`TOOLCHAIN=stock` / `ORACLE=1`）；
4. `GOTRACEBACK=crash` 生成的完整栈（可选）。
