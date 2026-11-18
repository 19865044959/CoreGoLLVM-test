---
id: l09_cgo_const_string
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 库返回 const char*）
tags: [L09, cgo, const-char*, GoString, copy]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库返回 `const char *`：静态版本串、空串、字符串查找表、300 字节静态长串，以及 malloc 副本。Go 用 `C.GoString` 复制后跨 GC 读取；副本 free 后 Go 字符串仍完整。

## 期望行为

1. 静态版本串/空串/查找表三项与预期一致；
2. 静态指针两次调用地址相同（不打印地址）；
3. 300 字节长串按 `a..z` 周期生成，GoString 逐字节一致且长度 300；
4. malloc 副本 GoString 后立即 free，GC 后 Go 字符串仍完整（复制语义）；
5. 32 轮 GC 后长串副本不变；64 轮 GC 后静态查找表仍可读；
6. NULL 静态指针的 GoString 返回空串；最后一行 `ALL PASS l09_cgo_const_string`。

## 失败判读

- 段错误：把 const 静态数组放到只读段后又写入（本用例已改为可写 static 数组）；
- free 后内容损坏：误以为 GoString 是零拷贝（实际是复制）；
- 长度错误：C 串缺少 NUL 或 GoString 使用了错误 API；
- 构建失败：preamble 中含函数定义但同文件有 //export（本用例无 //export）。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_const_string
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
