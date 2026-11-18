---
id: l09_cgo_string_array
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 侧 argv/字符串表是常见边界）
  - docs/research/tailscale.md:68（cgo/内核边界需真实 C 路径验证）
tags: [L09, cgo, string-array, char**, GoString, NULL-terminated, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C API 的字符串数组（`char **`，NULL 结尾，类似 `argv`）：覆盖静态只读表、C `malloc` 的指针数组 + 每个字符串单独 `malloc`（含空串与 200 字节长串），以及 Go 用 `C.CString` 组装后传给 C 的数组。Go 用 `unsafe.Slice` 得到 `[]*C.char` 并用 `C.GoString` 复制，或通过 C 索引辅助函数读取；C 只在调用期内访问指针，不保留 Go 指针。

# 期望行为

1. 静态数组元素数、NULL 结尾、每个字符串内容与 C 索引访问一致；
2. 动态数组 12 个元素（空串、普通串、200 字节长串）内容正确，越界/负索引返回 NULL；
3. C 计算的 `count` 与拼接长度（含分隔符）与 Go 参考一致；
4. 8 轮显式 GC 后，静态/动态数组的 C 内容仍可读且正确；
5. Go 组装的 `char **`（Go 数组 + C 字符串）可被 C 正确计数/求长，随后每个 `C.CString` 都能安全释放；
6. 最后一行 `ALL PASS l09_cgo_string_array`。

# 失败判读

- `NULL terminated`：C 数组长度或哨兵错误，Go 侧多读/少读一项；
- `dynamic[i]`：字符串内容被截断或指针数组错位（注意空串仍须是非 NULL 指针）；
- GC 后内容错误：C 内存被提前释放，或 Go 把 `C.char` 指针当普通整数存放导致丢失；
- Go-built count 错误：`&carr[0]` 的 NULL 哨兵缺失，或 C 误存了 Go 指针；
- 崩溃：释放了静态数组，或重复释放 `C.CString`。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_string_array
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
