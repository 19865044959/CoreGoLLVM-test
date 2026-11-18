---
id: l14_inline_callline_attribution
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_inline_callline_attribution/main.go:55 - 可内联叶子 ilBoom
  - cases/L14_debug_info/l14_inline_callline_attribution/main.go:57 - 单行调用 ilMid
  - cases/L14_debug_info/l14_inline_callline_attribution/main.go:59 - 单行调用 ilHostA
  - cases/L14_debug_info/l14_inline_callline_attribution/main.go:146 - 内联行号断言
  - docs/DIMENSIONS.md:22 - L14 调试信息（内联栈/行号）
tags: [L14, inline, line-number, traceback, CallersFrames, gc]
requires: []
runtime: "<3s"
---

# 场景

`ilBoom -> ilMid -> ilHostA -> ilHostC` 都是由一次单行调用构成的可内联链，`ilHostB` 从另一源码行调用同一个 `ilMid`。panic 后 traceback 通过内联树展开每个逻辑帧，每帧必须带自己的源码行：panic 行 < ilMid 调用行 < 宿主调用行；同一 ilMid 从不同宿主进入时行号固定，宿主调用行随调用点变化。对应内联优化打开时崩溃日志行号错位的问题。

## 期望行为

1. 各调用链的 traceback 以 `main.ilBoom(...)`、`main.ilMid(...)`、`main.ilHostA(...)` 等无偏移形式标记内联帧；
2. 帧序由内向外严格为 `ilBoom < ilMid < ilHostA < ilHostC`；
3. 三层链满足 boom.Line < mid.Line < hostA.Line，四层链继续 hostA.Line < hostC.Line；
4. 同一内联函数的 File 均以 `main.go` 结尾、Line>0，且能解析到真实宿主 Entry；
5. 另一宿主 ilHostB：ilBoom 行号与 ilMid 行号与 hostA 链相同，ilHostB 调用行不同且大于 ilMid 行；
6. 每个内联符号在 traceback 中只展开一次，不丢帧不重复；
7. GC 后 payload 链有效，最后一行 `ALL PASS l14_inline_callline_attribution`。

## 失败判读

- 栈中显示 `main.ilBoom(0x...)` 且带偏移：函数未被内联（构建参数问题）或内联帧未标记；
- 多帧共用同一行号：内联行号表未按调用点生成；
- 帧序颠倒或某帧缺失：内联树展开错误；
- 同一符号出现两次：traceback 内联帧重复。

## 参数

无随机路径。函数体保持小规模以确保 Go 1.24.1 默认内联；`ilRun` 为 noinline 恢复入口。断言依赖相对行号关系与内联标记，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_inline_callline_attribution/*.go
GODEBUG=clobberfree=1 /tmp/case
# 如需确认内联决策：go build -gcflags=-m ...
```
