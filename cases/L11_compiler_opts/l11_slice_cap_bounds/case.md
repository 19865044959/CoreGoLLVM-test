---
id: l11_slice_cap_bounds
dim: L11_compiler_opts
priority: P2
sources:
  - klauspost-compress@v1.17.11: s2/s2_test.go:161 TestSmallCopy dst 容量 nil/20/64 三档
  - klauspost-compress@v1.17.11: s2/s2_test.go:493 TestDecodeLengthOffset 容量内哨兵检测越界写
  - goccy-json@v0.10.5: decode_test.go:3128 TestSliceElemUnmarshaler 切片收缩/复用底层数组
tags: [slice-cap, three-index-slice, append, capacity-sentinel, bounds-check, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 6 轮；TORTURE_ROUNDS=100 时 600 轮）"
---

# 场景

模拟压缩解码目标缓冲与 JSON 切片复用中的容量语义：`buf[2:5:6]` 的三索引 len/cap、容量内 append 共享底层数组、超过 cap 后重新分配；满容量切片 append 不得覆盖同底层的兄弟切片；`guard[:8:12]` append 4 个元素后只写 8..11，12..15 哨兵必须不变；`guard[:cap(guard)]` 允许在 cap 内重切；重叠 `copy` 的 memmove 语义。每轮按 `UAF_SEED` 生成 16..31 长度的缓冲与切点，验证子切片求和、append 写位置与 GC 后的底层数据。非法三索引/越界切片必须 panic 且含 `out of range`。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. `buf[2:5:6]` 长度为 3、cap 为 4，元素与底层数组共享；
2. `cap(buf[3:]) == 17`、`cap(buf[3:7]) == 17`、`cap(buf[3:7:9]) == 6`；
3. 容量内 append 保持底层地址并写入索引 5；超出 cap 的 append 重新分配，原数组不变；
4. 满容量切片 append 不复用原底层，兄弟切片首元素保持原值；
5. `guard[:8:12]` append 后 `guard[12..15]` 哨兵不变；
6. 重叠 copy 得到 `[0,0,1,2,3,4]`；
7. max<high、max>cap、low>high、high>cap、负下标全部 panic 且文本含 `out of range`，recover 后原缓冲不变；
8. 子切片、底层地址、哨兵与增长结果在 GC 后保持；
9. 每轮随机切片的 len/cap/求和/append 位置正确；
10. 最后一行输出 `ALL PASS l11_slice_cap_bounds`。

# 失败判读

- `three-index len/cap` / `cap arithmetic`：三索引切片长度/容量计算错误；
- `append within cap` / `append beyond cap`：append 是否复用的判定错误或越界写；
- `full-slice cap prevents sibling overwrite`：满容量保护失效，写坏同底层切片；
- `capacity sentinel untouched`：append 写出 len/cap 边界；
- `expected slice panic` / `unexpected panic text`：非法切片未 panic 或边界检查被错误删除；
- `... after GC`：底层数组被回收/破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮缓冲长度 16..31 与切点，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（6 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_slice_cap_bounds/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=7 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_slice_cap_bounds/*.go
UAF_SEED=1 /tmp/case_n
```
