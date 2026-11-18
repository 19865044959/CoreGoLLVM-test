---
id: l09_cgo_realloc_structs
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 内存生命周期）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，裸 C 内存管理）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, realloc, struct-array, prefix-preservation, FNV, alignment, C-heap, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 cgo 绑定层用 `realloc` 维护含内嵌 `char[16]` 的记录数组：从 `realloc(NULL,4)` 开始，依次增长到 9、同尺寸重分配、收缩到 3、增长到 37/257、收缩到 1、再增长到 1000。每一步新扩展区先由 C 填充，Go 才通过 `unsafe.Slice` 读取；校验 `max_align_t` 对齐、逐字段内容、C/Go 双侧 FNV-1a 字节哈希一致、收缩前 3 条记录在后续所有步骤中保持不变。Go 还直接改写 C 内存中一条记录的 flags，由 C 侧校验器定位到该下标。数组全程在 C 堆，C 不保留 Go 指针。

# 期望行为

1. `sizeof(rs_rec_t)`、`alignof(rs_rec_t)`、`offsetof(name/len)` 与 Go 的 unsafe 值一致，`alignof(max_align_t)>=8`；
2. 每次 `realloc`（含 NULL 首次、同尺寸、收缩、增长）都成功且返回地址满足 `max_align_t` 对齐；
3. 每条记录满足 `id=i`、`flags=(i*37+11)&0xff`、`len=i%15`、`pad=0`、`name="rec-%03d"`（i%1000）；
4. 每一步 C `rs_verify` 返回 -1，Go 逐字段校验通过，C 与 Go 对整段原始字节的 FNV-1a 64 哈希相同；
5. 收缩到 3 之后，前 3 条记录的哈希在增长到 37/257/1000 后仍保持不变（realloc 前缀保留）；
6. 显式 GC 后内容与哈希不变；
7. Go 写入 flags=0x5a5a 后 C 读回一致，且 `rs_verify` 报坏下标 500；C 重新填充该记录后校验恢复 -1；
8. 退出码 0，最后一行 `ALL PASS l09_cgo_realloc_structs`。

# 失败判读

- `realloc NULL`：内存不足或尺寸计算溢出，检查 n；
- 对齐失败：realloc 返回未按 `max_align_t` 对齐的地址（C 堆损坏）；
- 字段/名字不符：扩展区未由 C 初始化就被 Go 读取，或 offsetof 映射错误；
- FNV 不一致：Go/C 对结构体布局或填充字节（pad）理解不同；若差异仅在 padding，检查 C 侧是否 memset；
- `first 3 records preserved` 失败：realloc 未保留前缀（实现异常）或收缩后又误填了旧下标；
- `verify detects Go mutation` 未报 500：C 校验器下标/字段逻辑错误；
- 崩溃：realloc 后仍使用旧指针（未接收返回值）。

# 参数

- 无随机路径；尺寸序列固定为 4→9→9→3→37→257→1→1000；`UAF_SEED` 仅由 runner 注入。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_realloc_structs
go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
