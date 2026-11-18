---
id: l14_callers_skip
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_callers_skip/main.go:53 captureSkip 内直接调用 runtime.Callers
  - cases/L14_debug_info/l14_callers_skip/main.go:72 levelC 调用层
  - cases/L14_debug_info/l14_callers_skip/main.go:78 levelA 最外层调用
  - docs/DIMENSIONS.md:22 L14 调试信息（CallersFrames）
tags: [L14, runtime.Callers, skip-semantics, CallersFrames, symbol-resolution, gc]
requires: []
runtime: "<3s"
---

# 场景

`main -> levelA -> levelB -> levelC -> captureSkip -> runtime.Callers` 的固定 noinline 链上逐个测试 skip 值：skip=0 应指向 runtime.Callers 自身，skip=1 指向 captureSkip，之后逐层外移；越界 skip 返回 0 帧。每帧还校验文件/行号/Entry 符号。

## 期望行为

1. first frame：skip=0 为 `runtime.Callers`，skip=1 为 `main.captureSkip`，skip=2 为 `main.levelC`，skip=3 为 `main.levelB`，skip=4 为 `main.levelA`；
2. skip=1000 返回 0 帧；
3. skip=2 的帧序列前两帧为 levelC、levelB；skip=4 前两帧为 levelA、main；
4. skip=2 中所有 main 帧的 File 以 `main.go` 结尾、Line>0，且 `runtime.FuncForPC(Entry).Name()` 等于帧函数名；
5. skip=0 的 runtime.Callers 帧有正行号；
6. 退出码 0，最后一行 `ALL PASS l14_callers_skip`。

## 失败判读

- skip 偏移一位：runtime.Callers skip 语义实现错误；
- 越界 skip 非 0 帧：边界处理错误（CallersFrames 空输入被误当作一帧）；
- Entry 名称不一致：PC 到符号表映射错误；
- 行号为 0：行号表缺失。

## 参数

无随机路径；全部调用层 `//go:noinline`，skip 偏移稳定。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_callers_skip/*.go
GODEBUG=clobberfree=1 /tmp/case
```
