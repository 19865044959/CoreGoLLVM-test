---
id: l09_cgo_memcpy
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 内存操作是常见边界）
  - docs/research/tailscale.md:68（cgo/内核边界需真实 C 路径验证）
tags: [L09, cgo, memcpy, sentinel, struct-array, malloc, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定中最常见的 C `memcpy` 路径：Go slice 中间区间复制（前后哨兵检测越界写）、零长度复制、源子区间复制、64 KiB 大批量复制 + FNV-1a 校验、Go→C malloc 缓冲→Go 往返、以及 `rec_t` 结构体数组批量复制。所有复制区间互不重叠；C 只在调用期内访问指针，返回后不保留任何 Go 指针。

# 期望行为

1. 中间区间复制结果等于源，目标前后哨兵、源前后哨兵均未被改写；
2. 零长度 `memcpy` 不修改目标；子区间复制只改目标前 8 字节；
3. 64 KiB 复制内容与哈希一致，首尾字节一致；
4. C malloc 缓冲往返复制后内容/哈希一致，随后可安全 `free`；
5. `rec_t` 数组 40 条记录的 `a/b/c` 字段全部正确复制；
6. 8 轮显式 GC 后所有副本、C 缓冲内容仍正确；
7. 最后一行 `ALL PASS l09_cgo_memcpy`。

# 失败判读

- `prefix/suffix sentinel`：复制长度或目标偏移错误，越界写；
- `zero-length` / `subrange`：`n=0` 未短路，或源偏移计算错误；
- `64KiB hash`：大块复制截断/错位；
- `roundtrip`：C malloc 缓冲与 Go 内存指针传递错误；
- `rec[i]`：结构体大小/布局不一致（`sizeof(rec_t)` 与 Go 端计算不符）；
- GC 后错误/崩溃：C 保留了 Go 指针，或副本被错误回收；
- 越界崩溃：`unsafe.Pointer` 偏移越过 slice 边界。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_memcpy
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
