---
id: l09_cgo_cstring
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/SOURCES.md:54（tailscale 适配 cgo/L09 场景）
tags: [L09, cgo, CString, GoString, CBytes, NUL]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 库中最常见的字符串边界：`C.CString` 分配、`C.GoString` 回读、`C.CBytes` + `C.GoStringN` 保留内嵌 NUL。覆盖 ASCII、空串、UTF-8、200 字节 ASCII 串，以及 32 个 C 字符串同时存活跨 GC 后逐一校验、释放；内嵌 NUL 由 CBytes/GoStringN 单独覆盖。

# 期望行为

1. ASCII/空串/UTF-8 往返一致，`strlen` 等于 Go 字符串字节长度；
2. `CBytes` 写入 `{'a',0,'b',0xff,'c'}` 后 `GoStringN` 原样返回 5 字节，`GoString` 在第一个 NUL 处截断为 `"a"`；
3. `C.GoString(nil)` 返回空串；
4. 32 个 C 字符串跨 GC 后内容完好，随后全部释放；
5. 200 轮 CString/GoString/free 交替 + 周期 GC 无错误；
6. 最后一行 `ALL PASS l09_cgo_cstring`。

# 失败判读

- `GoString` 截断/乱码：C 侧缺少结尾 NUL，或长度参数错误；
- `GoStringN` 丢失内嵌 NUL：误用 `GoString`（其语义就是截断，属预期）；
- 崩溃：C 指针被重复释放或未释放导致泄漏（本用例配对 free）；
- GC 后字符串损坏：C 字符串被 Go GC 误回收（C 内存不在 Go 堆，正常应稳定）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_cstring
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
