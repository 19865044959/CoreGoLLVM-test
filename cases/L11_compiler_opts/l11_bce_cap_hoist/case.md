---
id: l11_bce_cap_hoist
dim: L11_compiler_opts
priority: P2
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:10 TestGetCap 容量按 2 的幂分档
  - pgx@v5.7.4: internal/pgio/write_test.go:23 TestAppendUint16BufWithCapacityDoesNotAllocate cap 足够时不重新分配
  - klauspost-compress@v1.17.11: s2/s2_test.go:161 TestSmallCopy nil/20/64 三档目标容量
tags: [bounds-check-elimination, slice-cap, three-index-slice, reslice, loop-hoisting, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 6 轮；TORTURE_ROUNDS=100 时 600 轮）"
---

# 场景

模拟容量分档缓冲与压缩小目标缓冲中的 cap 相关边界：每轮生成 24..63 字节、容量多 8..39 的底层数组，在尾部填充哨兵；构造 `raw[off:off+w:off+w]` 三索引窗口，把切片重切到 `cap` 后做整段求和，用相邻四下标与循环内 `raw[:i]` 重切片做大热循环，并保留完整 cap 视图跨 GC 逐字节复核。另验证超出 cap 的切片、窗口越界、low>high、负下标、等于 cap 的下标全部 panic 且文本含 `out of range`。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 三索引窗口 `len==cap==w`，内容与同区间子切片求和一致；
2. `raw[:n]` 重切到 cap 后长度等于容量，整段和等于正文和 + 哨兵和；
3. 相邻四下标求和与逐元素参考实现一致；
4. 循环内 `raw[:i]` 的末元素和等于正文和；
5. 五类非法切片/下标全部 panic，recover 后底层数组不变；
6. 每轮保留的 cap 视图在最终 GC 后逐字节不变；
7. 默认 6 轮，实际轮数为 `6 * min(TORTURE_ROUNDS,100)`；
8. 最后一行输出 `ALL PASS l11_bce_cap_hoist`。

# 失败判读

- `three-index window len/cap` / `reslice to cap`：三索引或 cap 派生长度计算错误；
- `cap-derived loop sum` / `adjacent quadruple` / `reslice inside loop`：边界检查消除后循环越界或漏读；
- `expected panic` / `unexpected panic text`：越界未 panic 或 panic 来源错误；
- `retained capacity view corrupted after GC`：底层数组被回收/改写；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮长度 24..63、额外容量、窗口宽度与偏移，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（6 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_bce_cap_hoist/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_bce_cap_hoist/*.go
UAF_SEED=1 /tmp/case_n
```
