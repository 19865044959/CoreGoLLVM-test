---
id: l11_const_float_edges
dim: L11_compiler_opts
priority: P0
sources:
  - goccy-json@v0.10.5: encode_test.go:1094 TestMarshalFloat 浮点编码往返
  - goccy-json@v0.10.5: encode_test.go:1100 float32 结果先舍入到 float32 再比较
  - goccy-json@v0.10.5: encode_test.go:1155 math.Nextafter/Nextafter32 相邻浮点边界
  - go-redis@v9.8.0: internal/hscan/hscan_test.go:111 math.MaxFloat32 与 :112 math.MaxFloat64 扫描边界
tags: [const-fold, float, float32, ties-to-even, subnormal, NaN, infinity, negative-zero, map-key, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 JSON 浮点编码与 Redis 扫描中的 IEEE 边界：2^53 与 2^24 附近的 ties-to-even 常量转换、高精度十进制常量单次舍入、float32 半路舍入、次正规数的运行期除 2 下溢与乘 2、±Inf/NaN 比较、常量与运行期类型差异、float 作为 map 键以及保留位模式跨显式 GC。

# 期望行为

1. `2^53+1` 舍入到偶数的 `2^53`，`2^53+3` 舍入到 `2^53+4`，`2^53+2` 精确；
2. 0.1 的高精度十进制常量展开转换为 `0x3fb999999999999a`，与字面量 0.1 位模式一致；
3. float32 在 `1+2^-24`、`1+3*2^-24`、`2^24+1`、`2^24+3` 处均按 ties-to-even 舍入；
4. 常量负最小次正规数保留符号位，常量最小次正规数位模式为 0x1；
5. 运行期最小次正规数除以 2 下溢为 0，乘 2 得到位模式 0x2；float32 同理；
6. `+Inf > MaxFloat64`、`-Inf < -MaxFloat64`，Inf 相等/排序正确；NaN 不等于自身、无序且位模式非零；
7. 无类型常量 `1/2` 为 int、`1/2.0` 为 float64；运行期 float→int 向零截断；
8. 浮点 map 键命中，Inf 键普通，NaN 键永不命中；
9. 保留的 float64/float32 位模式、次正规数、负零与 NaN 在显式 GC 后不变；
10. 最后一行输出 `ALL PASS l11_const_float_edges`。

# 失败判读

- `2^53+... ties to even` / `float32 halfway`：常量转换的舍入方向错误（截断或错误 tie 规则）；
- `exact 0.1 decimal constant`：高精度常量被提前舍入；
- `smallest subnormal half underflows`：运行期次正规数下溢处理错误；
- `NaN not equal` / `NaN map key never matches`：NaN 比较或哈希语义被改写；
- `untyped constant result types`：常量默认类型被误解；
- `retained float bits after GC`：浮点数据被回收或位模式损坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定累加用的浮点 salt 1..97，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P0，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_const_float_edges/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_const_float_edges/*.go
UAF_SEED=1 /tmp/case_n
```
