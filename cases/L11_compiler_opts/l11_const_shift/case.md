---
id: l11_const_shift
dim: L11_compiler_opts
priority: P0
sources:
  - fasthttp@v1.59.0: round2_64_test.go:10 切片容量向上取整到 2 的幂（移位/掩码常量）
  - fasthttp@v1.59.0: bytesconv_64_test.go:18 uint 十进制格式化的位运算边界
  - klauspost-compress@v1.17.11: zstd/seqdec_test.go:17 prevOffset/offset/litLen/offsetB 组合的位移调整表
tags: [const-fold, shifts, big-int, arithmetic-shift, narrow-integer, wrapping, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟协议格式化与解码中的移位：任意精度常量移位（`1<<100`、`-1<<100`、`(1<<63)-1`）、常量移位作为数组长度/下标/`case` 标签/map 键、`int64`/`uint64`/`int8`/`uint8` 在 0..127 位移下的结果、有符号右移的符号填充、超宽移位计数的零/符号结果，以及运行期负移位计数 panic。运行期结果逐项与 `math/big` 独立参考对照，常量数组与保留结果跨 GC 复核。

# 期望行为

1. 常量移位值 `16`、`3072`、`-40`、`-8`、`2^63-1` 正确；
2. `1<<100>>100==1`、`>>99==2`、`>>64==1<<36`、`-1<<100>>100==-1`；
3. 常量长度数组为 32 字节，常量下标与 `case` 标签命中正确；
4. `int64/uint64` 在 13 个移位计数下与 `big.Int` 参考逐项相等；
5. `int8/uint8` 在 10 个移位计数下按类型截断/符号扩展，与参考相等；
6. 移位计数 `^uint(0)` 时左移为 0，有符号右移为 0 或 -1，不 panic；
7. 运行期负移位计数 panic，文本含 `negative shift amount`；
8. 常量数组、二维常量维度数组与保留的移位结果在 GC 后不变；
9. 最后一行输出 `ALL PASS l11_const_shift`。

# 失败判读

- `const ... mismatch`：常量折叠/任意精度移位结果错误；
- `int64/uint64/int8/uint8 ... shift mismatch`：运行期移位或截断语义与参考不符；
- `huge-count ...`：超宽移位计数未按零/符号填充处理；
- `negative shift count did not panic`：负移位计数漏检；
- `corrupted after GC`：常量数组/保留结果被越界写或回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定附加的随机被移位值与 uint64 乘子，失败输出带种子；
- 本用例为 P0，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_const_shift/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_const_shift/*.go
UAF_SEED=1 /tmp/case_n
```
