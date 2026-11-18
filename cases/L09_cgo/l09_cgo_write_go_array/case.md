---
id: l09_cgo_write_go_array
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 写 Go 数值缓冲并返回长度）
tags: [L09, cgo, go-array, length-return, LEB128, sentinel]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 编码器写 Go 定长数组并返回写入长度：固定容量填充最多 16 个 int32、指定偏移写入、LEB128 变长编码；Go 校验返回长度、尾部哨兵未被改写、容量不足返回 -1 且不越界。

## 期望行为

1. 32 元素数组填充返回 16，前 16 个为 `100+i*7`，其余保持哨兵；
2. cap=5/0/-1 分别返回 5/0/-1，未写区域不变；
3. 从 `&a3[3]` 偏移写入 5 个元素，仅 `[3,8)` 改变；count>cap 返回 -1 且不写；
4. 9 个 uint64 的 LEB128 编码长度 1..10，Go 解码往返一致，尾部哨兵 0xcc 不变；
5. 容量恰好成功、容量少 1 返回 -1 且不写；
6. GC 后重新填充数组结果正确；最后一行 `ALL PASS l09_cgo_write_go_array`。

## 失败判读

- 哨兵被改：C 写超过 cap/count，或偏移指针计算错误；
- 返回长度错误：C 的 int32 返回与 Go 转换不一致；
- 变长编码往返失败：C 的 LEB128 continuation 位或移位错误；
- 容量不足仍写入：C 未在每个字节写前检查 cap；
- `cgo argument has Go pointer to Go pointer`：向 C 传了含指针的 Go 内存（本用例只传数值数组）。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_write_go_array
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
