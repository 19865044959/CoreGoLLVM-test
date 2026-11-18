---
id: l09_cgo_return_ptr_len
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09；C 返回缓冲指针+长度是常见 ABI）
tags: [L09, cgo, ptr-len, unsafe.Slice, malloc, NULL]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库“返回指针 + 长度”的三种形态：`malloc` 数组（调用方负责 `free`）、静态数组（不可 free）、静态字符串（长度经出参返回）。Go 侧用 `unsafe.Slice` / `C.GoStringN` 读取，并在 GC 后复核 C 内存；另外验证 NULL 返回值与零长度切片。

# 期望行为

1. `c_make_i64s(64, -100)` 返回非 NULL，64 个元素为 `-100+i*7`，GC 后不变，随后释放；
2. 静态数组返回 `[10,20,30,40]`，GC 后不变；
3. `c_message` 返回长度 11、内容 `"cgo-ptr-len"`，GC 后不变；
4. `c_null_ptr()` 返回 nil，`unsafe.Slice(NULL, 0)` 长度为 0；
5. 100 轮 malloc/GC/读取/free 每轮首尾元素正确；
6. 最后一行 `ALL PASS l09_cgo_return_ptr_len`。

# 失败判读

- GC 后内容变化：C 内存被误纳入 Go 堆管理（不应发生）；
- 段错误：忘记 free 导致泄漏不致命；重复 free/读已释放内存会崩溃（本用例配对操作）；
- 长度错误：C 出参未写或 Go 将 `size_t` 转 `C.int` 时溢出（本用例长度仅 11）；
- NULL 处理失败：对空指针做非零长度 `unsafe.Slice` 会 panic（本用例只取长度 0）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_return_ptr_len
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
