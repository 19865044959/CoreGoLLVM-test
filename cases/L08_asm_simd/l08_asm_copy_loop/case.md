---
id: l08_asm_copy_loop
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:35（s2 汇编 expand/copy 的越界写不变量）
tags: [L08, asm, amd64, SSE2, copy, sentinel]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩库内“汇编块拷贝”循环：按 16 字节 `MOVOU`、8 字节、单字节三层推进，只写 `[dst, dst+n)`。Go 侧用首尾哨兵字节检测越界写，并在 GC 后复核源与目标。

# 期望行为

1. n=0..70 全部长度返回 n，且目标内容与源完全一致；
2. `dst[n+1]` 哨兵与 `dst[0]` 哨兵不被改写（无越界写）；
3. 原地拷贝 `dst==src` 内容保持不变；
4. 每轮 GC 后内容不变；
5. 最后一行 `ALL PASS l08_asm_copy_loop`。

# 失败判读

- 内容尾部错误：16B/8B/1B 分层循环的剩余长度分支错误；
- 哨兵被改：越界写（汇编写超过 n 字节）；
- 原地拷贝损坏：实现未按正向拷贝或读写在同址时顺序有误；
- GC 后内容错误：Go 侧切片被回收（缺少 KeepAlive）或参数栈映射缺失。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。本实现不支持重叠区间拷贝。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_copy_loop
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
