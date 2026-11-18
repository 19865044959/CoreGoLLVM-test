---
id: l11_bounds_check
dim: L11_compiler_opts
priority: P1
sources:
  - klauspost-compress@v1.17.11: s2/s2_test.go:611 纯 Go 模拟 expand pattern，验证越界写不超过 10 字节
  - klauspost-compress@v1.17.11: zstd/zstd_test.go:45 129 字节逐位翻转，校验 matchLen 边界 0..128
  - klauspost-compress@v1.17.11: s2/s2_test.go:161 dst 容量 nil/20/64 三档 × 32 种长度，覆盖扩容与小块路径
tags: [bounds-check-elimination, panic-recover, slice-bounds, loop-hoisting, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟压缩/解析库中大量 `for i := 0; i < len(s); i++ { s[i] }` 热循环与切片窗口运算：长度由 `UAF_SEED` 在 64..95 间选取，分别用下标循环、range 循环、双子切片配对循环求和并互相对照；再构造 6 组越界索引/切片（`s[len]`、负下标、`s[5:3]`、`s[:len+1]`、nil 切片、零长切片），用 recover 判读 panic 文本必须含 `out of range`，且数组内容与长度不受影响。16 轮临时切片求和穿插 GC。

# 期望行为

1. 下标循环与 range 循环对同一非 nil 切片求和相等，nil/空切片求和为 0；
2. 末元素、`s[1:len-1]`、`s[n-1:]` 读取值正确，子切片和等于总和减去首尾；
3. 配对循环在 `min(len(a),len(b))` 范围内求和正确，clamp 切片长度正确；
4. 6 组越界操作全部 panic 且 panic 文本含 `out of range`，recover 后原切片长度/内容不变；
5. 16 轮“新建切片 + 求和 + 每 4 轮 GC”的累计和等于解析公式，最终切片不被破坏；
6. 最后一行输出 `ALL PASS l11_bounds_check`。

# 失败判读

- `sum mismatch` / `two loop forms disagree`：边界检查消除改变了合法下标访问结果；
- `expected bounds panic`：越界访问未触发 panic（边界检查被错误删除）；
- `unexpected panic text`：panic 类型/来源错误（可能是 nil 解引用而非越界）；
- `slice corrupted after GC stress`：切片底层数组在 GC 后被破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定切片长度 `64 + seed%32`，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定 16 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_bounds_check/*.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_bounds_check/*.go && UAF_SEED=7 /tmp/case0
```
