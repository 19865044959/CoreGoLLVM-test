---
id: l11_bce_multi_index
dim: L11_compiler_opts
priority: P2
sources:
  - klauspost-compress@v1.17.11: s2/s2_test.go:493 length/offset 1..18 组合解码不得写到 len(dst) 之外
  - klauspost-compress@v1.17.11: snappy/snappy_test.go:412 length/offset/suffix 三重边界组合
  - fasthttp@v1.59.0: header_test.go:2368 十万 header 上限与每次只返回 1 字节的增量解析
tags: [bounds-check-elimination, multi-index, adjacent-index, reverse-index, slice-copy, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 8 轮；TORTURE_ROUNDS=50 时 400 轮）"
---

# 场景

模拟编解码与协议解析中的多下标循环：同一轮内访问 `a[i]` 与 `b[i+1]`、正向 `a[i]` 配反向 `b[n-1-i]`、连续三个下标 `s[i-1]/s[i]/s[i+1]`、每轮连续四个下标、同一切片的 `s[i]` 与 `s[(i*k+7)%n]`、窗口首尾 `s[i]/s[i+w-1]`、三切片同下标写读，以及 `copy(dst, src[k:])` 平移拷贝。每轮规模与 salt 由 `UAF_SEED` 决定，结果与独立参考循环及闭式公式对照，所有轮次切片保留到最终 GC 后复算。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 相邻双下标结果等于平移索引参考实现与闭式公式；
2. 正反向配对结果等于反向索引参考与 `3*sumA-sumB`；
3. 三元窗口、四路步进、同切片双索引、窗口首尾均与独立参考一致；
4. 三切片同下标写读结果等于 `sumA+2*sumB`；
5. 平移拷贝与逐元素循环、`mixAll(src[k:])` 校验和一致；
6. 合法末下标与合法尾部四元素子切片可读；
7. 越界索引、负索引、`b[i+1]` 末轮越界、超长切片全部 panic 且文本含 `out of range`；
8. 每轮保留的切片在全部轮次与最终 GC 后复算结果不变；
9. 最后一行输出 `ALL PASS l11_bce_multi_index`。

# 失败判读

- `pair adjacent` / `reverse pair` / `adjacent triple` 等 mismatch：多下标边界检查消除改变了合法访问结果；
- `shifted copy`：切片平移边界计算错误；
- `expected panic` / `unexpected panic text`：越界未 panic 或 panic 来源错误；
- `retained ... corrupted after GC`：切片底层数组被回收/破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮长度、salt、双索引步长 `k` 与窗口宽度 `w`，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（8 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_bce_multi_index/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_bce_multi_index/*.go
UAF_SEED=1 /tmp/case_n
```
