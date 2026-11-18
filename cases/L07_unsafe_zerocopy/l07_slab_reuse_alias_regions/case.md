---
id: l07_slab_reuse_alias_regions
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - sonic@v1.13.2: internal/encoder/encoder_test.go:53 TestEncoderMemoryCorruption 编码缓冲边界/复用不得破坏
  - pgx@v5.7.4: pgtype/bytea_test.go:49 TestDriverBytes 结果缓冲复用，旧结果被后续写入覆盖
  - fasthttp@v1.59.0: args_test.go:483 TestArgsParse 池化 Args 反复 Parse/Reset 复用底层缓冲
tags: [L07, unsafe, buffer-reuse, slab, alias, snapshot]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

模拟 bytebufferpool/slab：`slab.buf` 被切成 8 字节对齐的多个子区间，分配器返回子切片，调用方用 `unsafe.String` 生成零拷贝视图，同时保留 `string(view)` 快照作对照。用例覆盖：子区间地址/长度与互不重叠、slab 满时分配失败、只保留 string 视图时底层缓冲跨 GC 保活、不同 slab 之间互不影响、同一 slab reset 后原地覆盖（alias 见新内容、快照保持旧内容）、半覆盖边界，以及对齐填充不串区。

# 期望行为

1. 每个区域的 len 等于对齐后的请求大小，分配偏移按 size 递增；
2. 零拷贝 alias 的数据指针等于 slab 基址 + 区域起始偏移，内容等于快照；
3. 区域起始位置严格等于前一区域起始 + 大小，互不重叠；
4. slab 满后再次分配返回 nil；
5. 丢弃 slab 变量、仅保留 string 视图后，GC 不改变任何视图内容；
6. 写入第二个 slab 不改变第一个 slab 的 alias/快照，两个 slab 的 data 指针不同；
7. 同一 slab `off=0` 后重新分配同一区域并覆盖：alias 观察到新内容，快照保持旧内容；两个视图指向同一地址；
8. 原地覆盖后跨 GC、以及 slab 变量丢弃后，alias/快照语义仍正确；
9. 半覆盖一个区域只改变被写的前半，后半内容不变；
10. 对齐填充下的相邻区域边界不串，最终字符串仍可读；
11. 最后一行 `ALL PASS l07_slab_reuse_alias_regions`。

# 失败判读

- 区域地址/偏移不符：对齐或步长计算错误；
- 相邻区域互相污染：分配越界或写入超出区域长度；
- GC 后 alias 内容错乱/efence 崩溃：string 视图未保活 slab 底层数组；
- 原地覆盖后 alias 仍是旧内容：字符串被意外复制（零拷贝失效）；
- 快照随覆盖变化：快照实际是别名，未复制；
- 仅 stress/k8s 失败：GOGC=1 下视图/缓冲存活问题。

# 参数

- `UAF_SEED`：决定每轮记录标签与起始 round，失败信息带种子；
- `TORTURE_ROUNDS`：slab 轮数，默认 48；每轮多次 GC，放大可做长压测。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_slab_reuse_alias_regions/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=2000 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
