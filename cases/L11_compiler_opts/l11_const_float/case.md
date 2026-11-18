---
id: l11_const_float
dim: L11_compiler_opts
priority: P0
sources:
  - goccy-json@v0.10.5: encode_test.go:1094 TestMarshalFloat 浮点往返与 :1100 的 float32 舍入
  - goccy-json@v0.10.5: encode_test.go:1138 math.Inf(+1)/Inf(-1) 边界比较
  - go-redis@v9.8.0: internal/hscan/hscan_test.go:111 math.MaxFloat32 与 :112 math.MaxFloat64 扫描边界
tags: [const-fold, float, float32, complex, conversion, rounding, negative-zero, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 JSON 浮点编解码与字段扫描中的浮点常量：无类型常量 `0.1+0.2`、`0.1+0.2-0.3`、`1/3` 按任意精度计算并只舍入一次，而 `//go:noinline` 函数内的 float64 运算按 IEEE 逐步舍入，二者位模式必须分别正确；同时校验 float32 常量舍入、2^53+1 的偶舍入、整数转换、复数常量乘法、MaxFloat32/MaxFloat64 常量、常量 -0.0 与运行期负零，以及 map 浮点键和 GC 后保留切片的位模式。

# 期望行为

1. 常量 `0.1+0.2` 的 float64 位模式为 `0x3FD3333333333333`；
2. 运行期 `0.1+0.2` 位模式为 `0x3FD3333333333334`，与常量值不等；
3. 常量 `0.1+0.2-0.3` 精确为 0，运行期同类运算非零；
4. 常量 `1/3` 位模式为 `0x3FD5555555555555`，与运行期 `1.0/3.0` 相等；
5. `float32(1/3)` 位模式为 `0x3EAAAAAB`，`float32(0.1)` 位模式为 `0x3DCCCCCD`；
6. `float64(2^53+1)` 按偶舍入到 2^53，运行期 int64→float64 结果相同；
7. 运行期 float→int 转换向零截断，数组长度 3；
8. 复数常量 `(1+2i)*(3-4i)` 等于 `11+2i`，与运行期 complex128 结果一致；
9. MaxFloat32/MaxFloat64 与 MaxFloat64/2 的位模式正确；
10. 常量 -0.0 转换为 +0，运行期负零保留符号位；
11. 常量浮点 map 键命中，运行期不同的 sum 不命中；
12. GC 后保留浮点切片的位模式、负零符号与数组内容不变；
13. 最后一行输出 `ALL PASS l11_const_float`。

# 失败判读

- `constant 0.1+0.2` / `runtime 0.1+0.2`：常量折叠未按任意精度或运行期被错误折叠；
- `decimal cancellation`：常量表达式被提前按 float64 舍入；
- `one third` / `float32`：常量舍入到窄类型错误；
- `2^53+1`：整数到浮点转换未按 round-to-even；
- `complex constant`：复数常量分量计算错误；
- `MaxFloat` / `negative zero`：边界常量或符号位处理错误；
- `retained float bits after GC`：保留浮点数据被回收/改写；
- 失败输出带 `UAF_SEED`。

# 参数

- 无随机路径，读取 `UAF_SEED` 仅用于失败输出；固定规模；
- `-N -l` 与默认优化下所有位模式必须相同。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_const_float/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_const_float/*.go
UAF_SEED=1 /tmp/case_n
```
