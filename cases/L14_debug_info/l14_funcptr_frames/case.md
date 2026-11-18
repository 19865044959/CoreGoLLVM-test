---
id: l14_funcptr_frames
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_funcptr_frames/main.go:82 fpLeafA 目标函数
  - cases/L14_debug_info/l14_funcptr_frames/main.go:88 fpInvoke 间接调用
  - cases/L14_debug_info/l14_funcptr_frames/main.go:104 方法值目标
  - cases/L14_debug_info/l14_funcptr_frames/main.go:118 函数值/函数表/结构体字段/方法值四条路径
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开）
tags: [L14, func-pointer, method-value, reflect-funcval, traceback, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

分别通过普通函数值、函数表下标、结构体字段函数指针与方法值四种间接调用方式进入 panic 目标，验证 traceback 给出目标函数的真实符号而非调用者占位帧；并用 reflect funcval 反查普通函数值的符号。`TORTURE_ROUNDS` 放大轮数。

## 期望行为

1. 普通函数值 funcval 名称精确为 `main.fpLeafA`；
2. 四条路径的 traceback 分别包含 `main.fpLeafA`、`main.fpLeafB`、`main.fpLeafA`、`main.(*fpWorker).IndirectMethod`；
3. 函数表路径只出现被选中的 fpLeafB，不出现 fpLeafA；
4. 每条 traceback 都含 `main.fpInvoke`、`main.fpRun`、`goroutine` 头与 `main.go:<行号>`；
5. recover payload 身份正确；GC 后方法接收者/结构体字段中的指针链有效；
6. 退出码 0，最后一行 `ALL PASS l14_funcptr_frames`。

## 失败判读

- 间接调用只显示调用者符号：funcval/方法值符号解析错误；
- 函数表路径出现两个目标：下标分派或链接错误；
- 方法值帧缺失：方法值 wrapper 或方法符号生成错误；
- 行号缺失：调试信息生成缺失。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..16；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_funcptr_frames/*.go
TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
