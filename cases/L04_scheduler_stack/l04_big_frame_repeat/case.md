---
id: l04_big_frame_repeat
dim: L04_scheduler_stack
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/replacer_test.go:26 深占位符/转义放大到 10k–1M 触发 morestack 与栈复制
  - quic-go@v0.50.1: frame_sorter_test.go:1377 乱序/重复/重叠帧重组中的切片指针与 GC
tags: [L04, morestack, stack-copy, big-frame, stackmap, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 Caddy 深占位符展开与 QUIC 帧重组中“大栈帧 + 频繁 GC”的组合：`leafBigFrame` 每帧声明 8192 个指针槽（64KB），`nestedFrame` 每层声明 2048 个指针槽（16KB）并递归 4 层，最深处再调用 64KB 帧，整条栈约 128KB。所有槽位指向 64 个共享堆对象；GC 分别发生在 64KB 帧填满时与叶子返回后（父帧仍活跃时），随后逐槽比较指针身份并求和。

# 期望行为

1. `nestedFrame(rot, 3)` 的返回值等于独立计算的 `midSum(rot..rot+3) + frameSum(rot+3)`；
2. 每次 GC 后，8192 个槽位仍严格等于 `nodes[(i+rot)%64]`（指针身份，不仅是数值）；
3. 单独重复调用 64KB 帧时返回值与 `frameSum` 一致；
4. 三轮不同 `rot` 的结果一致，无 -2^60 哨兵值；
5. 最后一行输出 `ALL PASS l04_big_frame_repeat`。

# 失败判读

- 返回 -2^60：某个槽位的指针在栈复制/GC 后错位或指向错误对象；
- 指针身份不等但数值相等：栈映射把指针标成了标量或复制时未重定位；
- `SIGSEGV` / `found bad pointer`：大帧 stack map 漏标；
- 仅 k8s/stress 下失败：GOMAXPROCS=2 或高频 GC 下的栈复制路径问题。

# 参数

- `UAF_SEED`：节点值与轮转偏移种子（默认 1）；随机路径输出仅由种子决定。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_big_frame_repeat/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
