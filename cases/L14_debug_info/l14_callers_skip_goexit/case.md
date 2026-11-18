---
id: l14_callers_skip_goexit
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_callers_skip_goexit/main.go:61 - csCapture 逐层测试 skip
  - cases/L14_debug_info/l14_callers_skip_goexit/main.go:97 - 子 goroutine 入口 csEntry
  - cases/L14_debug_info/l14_callers_skip_goexit/main.go:139 - skip 0..6 期望函数表
  - cases/L14_debug_info/l14_callers_skip_goexit/main.go:155 - 越过 goexit 的 skip
  - docs/DIMENSIONS.md:22 - L14 调试信息（Callers skip）
tags: [L14, Callers, skip, goexit, goroutine, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

在子 goroutine 的固定 noinline 链 `csEntry -> csTop -> csMid -> csLeaf -> csCapture -> runtime.Callers` 上，逐个测试 skip=0..7 与 1000。子 goroutine 的栈底是 `runtime.goexit`：skip 恰好越过 goexit 时必须返回 0 帧。同时用单槽 PC 缓冲重复抓取，验证 skip 语义不因缓冲截断而改变。对应线上按 skip 跳过采集辅助帧、保留业务帧的栈采集代码。

## 期望行为

1. skip=0..6 的首帧依次为 `runtime.Callers`、`main.csCapture`、`main.csLeaf`、`main.csMid`、`main.csTop`、`main.csEntry`、`runtime.goexit`；
2. skip=k 时帧数为 7-k，末帧为 `runtime.goexit` 且行号 >0；
3. skip=7 与 skip=1000 返回 0 帧；
4. 单槽 PC 缓冲下 skip=0..6 仍得到对应首帧，第 7 个为 `runtime.goexit`；
5. skip=2 的 main 包帧 File 以 `main.go` 结尾、Line>0，Entry 反查与帧名一致，traceback 文本含 `main.go:<行号>`；
6. 两个不同 goroutine 上同一 skip 的函数序列与行号完全一致；
7. 显式 GC 后 payload 链有效，最后一行 `ALL PASS l14_callers_skip_goexit`。

## 失败判读

- 末帧不是 `runtime.goexit`：goroutine 栈底归属或帧展开错误；
- 越过 goexit 仍返回帧：skip 计数错误；
- 单槽缓冲下首帧改变：skip 与 PC 缓冲交互错误；
- 两次抓取行号不同：行号表不稳定。

## 参数

无随机路径。所有链上函数均 `//go:noinline`；断言依赖精确符号名与 skip 相对关系，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_callers_skip_goexit/*.go
GODEBUG=clobberfree=1 /tmp/case
```
