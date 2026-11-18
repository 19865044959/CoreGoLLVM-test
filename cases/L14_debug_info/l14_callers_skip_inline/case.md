---
id: l14_callers_skip_inline
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_callers_skip_inline/main.go:59 runtime.Callers 直接调用者
  - cases/L14_debug_info/l14_callers_skip_inline/main.go:78 预期被内联的单行叶子函数
  - cases/L14_debug_info/l14_callers_skip_inline/main.go:81 内联宿主函数
  - cases/L14_debug_info/l14_callers_skip_inline/main.go:93 skip 0..4 与内联帧展开断言
  - docs/DIMENSIONS.md:22 L14 调试信息（CallersFrames）
tags: [L14, runtime.Callers, skip-semantics, inlined-frame, CallersFrames, line-table, gc]
requires: []
runtime: "<3s"
---

# 场景

`main -> skipInlineOuter -> skipInlineMid -> skipInlineLeaf -> skipCapture -> runtime.Callers` 固定链上逐层测试 skip：`skipInlineLeaf` 是单行调用，预期被内联进 `skipInlineMid`。`runtime.Callers(skip)` 按逻辑帧（含内联帧）逐层跳过：skip=2 的首帧是内联叶子，skip=3 才到内联宿主。`CallersFrames` 展开后两个逻辑帧共享同一物理函数入口，但各自带独立的 `main.go` 行号。

## 期望行为

1. 首帧：skip=0 为 `runtime.Callers`，skip=1 为 `main.skipCapture`，skip=2 为 `main.skipInlineLeaf`，skip=3 为 `main.skipInlineMid`，skip=4 为 `main.skipInlineOuter`；
2. skip=1000 返回 0 PC / 0 帧；
3. skip=2 前两帧为叶子/宿主两个逻辑帧，二者 Function 不同、Entry 相同，且 Entry 解析为宿主符号；
4. 叶子 PC 解析为叶子符号、宿主 PC 解析为宿主符号；两帧 Line>0、行号不同、File 以 `main.go` 结尾；
5. skip 每增加 1 恰好少一个逻辑帧，skip=3 的首帧等于 skip=2 的第二帧；
6. 所有 main 帧的 File/Line/PC 到符号映射自洽；
7. 退出码 0，最后一行 `ALL PASS l14_callers_skip_inline`。

## 失败判读

- skip 首帧偏移：skip 按物理 PC 而非逻辑帧计数（内联展开不一致）；
- 内联叶子/宿主 Entry 不同或叶子符号缺失：内联帧调试信息缺失；
- 行号相同：内联帧未携带各自源码行；
- 越界 skip 仍返回帧：CallersFrames 空输入边界处理错误。

## 参数

无随机路径；叶子内联由默认编译选项决定（Go 1.24.1 稳定内联）；`UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_callers_skip_inline/*.go
GODEBUG=clobberfree=1 /tmp/case
```
