---
id: l09_cgo_snprintf_varargs
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 侧日志/格式串边界）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库调用边界）
tags: [L09, cgo, snprintf, varargs, formatting, truncation, C-heap, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定库对 printf 系列的固定参数包装：C 包装层把 int64/uint64/double/string 及混合参数转发给 `snprintf`，覆盖 `%lld/%llu/%x/%o`、宽度/精度/标志、浮点格式、`%s` 宽度截断、负零、返回值（所需长度）与缓冲截断/零容量语义；另验证 C 拥有缓冲区的格式化与跨 GC 稳定性。格式化只在调用期内写缓冲区，C 不保留 Go 指针。

# 期望行为

1. `MinInt64`、`MaxUint64`、`%+lld`、`%-6lld|`、`%06lld`、`%#010llx`、`%llo` 的输出与返回长度精确匹配；
2. 浮点 `%.2f/%05.2f/%e/%g/%.0f` 与负零 `-0.000000` 精确匹配（C locale 小数点）；
3. `%-8s|`、`%.3s`、`%10.4s`、空串的字符串格式正确；
4. 混合变参 `%d|%u|%.2f|%s` 输出 `-7|4000000000|2.75|ok`；
5. 截断时返回所需长度、缓冲写 cap-1 字节并以 NUL 结尾；恰好容纳时完整写入；
6. `cap=0` 不写内存只返回长度，`cap=1` 只写 NUL；
7. C `malloc` 缓冲区格式化后 Go 读回正确；
8. 显式 GC 后重复格式化结果一致；
9. 最后一行 `ALL PASS l09_cgo_snprintf_varargs`。

# 失败判读

- 内容/长度不符：包装层参数类型与格式符不匹配（例如把 int 传给 `%s`）；
- 浮点格式差异：C locale 被修改（本用例不调用 `setlocale`，默认 C locale）；
- 截断/零容量：对 snprintf 返回值与 NUL 终止语义理解错误；
- C 拥有缓冲区失败：malloc 指针/长度传递错误；
- GC 后失败：C 保留了 Go 指针（本用例格式串/字符串为 C.CString，缓冲写入在调用期内）；
- 崩溃：格式化写入越过缓冲区容量（本用例全程传 cap）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`；Linux/glibc。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_snprintf_varargs
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
