---
id: l14_loop_callsite_traceback
dim: L14_debug_info
priority: P0
sources:
  - cases/L14_debug_info/l14_loop_callsite_traceback/main.go:62 记录直接调用者行号后 panic
  - cases/L14_debug_info/l14_loop_callsite_traceback/main.go:72 循环内三个不同源码行的调用点
  - cases/L14_debug_info/l14_loop_callsite_traceback/main.go:92 从 traceback 解析宿主帧行号
  - cases/L14_debug_info/l14_loop_callsite_traceback/main.go:104 多轮迭代的行号稳定性断言
  - docs/DIMENSIONS.md:22 L14 调试信息（行号表）
tags: [L14, loop-lineinfo, callsite, traceback-attribution, line-table, gc]
requires: []
runtime: "<3s"
---

# 场景

循环体内同一 helper `loopPanicAt` 在 `switch` 的三个不同源码行被调用，每次调用都 panic 并在 defer 中 recover。`runtime.Caller(1)` 在 panic 前记录实际调用点行号；traceback 中宿主帧 `main.loopOne` 报告的行号必须与之一致，且同一 case 在 48 次迭代中保持稳定；三个调用点行号互不相同并按源码顺序递增（只比较相对关系，不依赖具体行号值）。

## 期望行为

1. 每次 panic 的 payload 与传入对象同一；
2. `runtime.Caller(1)` 报告的文件以 `main.go` 结尾、行号为正；
3. traceback 宿主帧 `main.loopOne` 的行号等于 `runtime.Caller` 记录的调用点行号；
4. 同一 case 跨迭代的行号/文件保持稳定；
5. 三个 case 的行号互不相同且 `site0 < site1 < site2`；
6. traceback 包含 `main.loopPanicAt(`、`main.loopOne.func1(`、`goroutine` 头与 `main.go:<行号>`；
7. GC 后循环 payload 与指针链仍有效；退出码 0，最后一行 `ALL PASS l14_loop_callsite_traceback`。

## 失败判读

- traceback 行号与 Caller 行号不一致：行号表把 PC 归属到错误源码行；
- 同一 case 行号漂移：循环体行号映射不稳定；
- 三个 case 行号相同或乱序：行号表缺少区分度；
- 宿主帧缺失：循环内调用点的帧调试信息缺失。

## 参数

无随机路径；固定 48 次迭代（16 次/调用点）；`UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_loop_callsite_traceback/*.go
GODEBUG=clobberfree=1 /tmp/case
```
