---
id: l09_cgo_memmove_overlap
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 内存操作边界）
tags: [L09, cgo, memmove, overlap, sentinel]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库重叠区间搬移：Go 把 `[]byte` 传给 C 的 `memmove`，覆盖前向/后向重叠、原地、非重叠与零长度；与 Go `copy`（memmove 语义）逐字节对照，并用区间外哨兵检测越界写。

## 期望行为

1. 10 组固定 dst/src/n 组合（含相邻重叠与远端重叠）结果与 Go copy 一致；
2. 目标区间外的前后字节保持不变；
3. 200 组定种子偏移矩阵（a,b 交错、n=1..120）与 Go copy 一致；
4. GC 后对 512 字节缓冲做前向重叠搬移，结果仍与 Go copy 一致；
5. 最后一行 `ALL PASS l09_cgo_memmove_overlap`。

## 失败判读

- 重叠方向错误：C 调用误用 memcpy（未定义行为）而非 memmove；
- 哨兵被改：n 或起始指针计算错误导致越界写；
- 矩阵尾部越界：a+n/b+n 边界过滤条件错误；
- GC 后错误：切片未保持存活（本用例传 Go 指针但 C 不保留）。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_memmove_overlap
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
