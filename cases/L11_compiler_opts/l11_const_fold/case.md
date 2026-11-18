---
id: l11_const_fold
dim: L11_compiler_opts
priority: P0
sources:
  - fasthttp@v1.59.0: round2_64_test.go:10 切片容量向上取整到 2 的幂（含 MaxInt32/MaxInt64-1 边界）
  - fasthttp@v1.59.0: bytesconv_64_test.go:18 uint 十进制格式化的位运算边界
  - kitex@v0.13.0: pkg/utils/int_len_test.go:21 GetUIntLen 对 0/5/10/56/102/99999 的位数表
tags: [const-fold, bitops, shifts, wrapping, constant-string, constant-array, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 URL 解析、整数格式化与容量取整里大量使用常量表达式/位运算的路径：一组 `uint64` 常量由移位、乘加、取模、按位与/异或、`&^` 取整构成，另含负整数算术右移、`uint32` 掩码、常量字符串长度/下标与 rune 常量。运行期用独立函数从 `1` 逐步移位重算同样的值并逐项对照；同时用常量长度声明数组、用常量网格尺寸做二维求和，在 GC 后校验堆上保存的常量序列与网格数据。

# 期望行为

1. `cA..cN` 与运行期重算值逐项相等（含 `1<<63`、`^uint64(0)>>7`、`(12345+4095)&^4095==16384`）；
2. 负常量 `-1024>>3 == -1024/8 == -128`（算术右移）；
3. 常量字符串 `len==10`、首字节 `'c'`、下标 5 为 `'-'`、`'A'+25=='Z'`；
4. `[160]int64` 常量长度数组与 `[16][16]int64` 网格求和正确，GC 后不变；
5. 运行期回绕（`uint8 255+1==0`、`int8 127+1==-128`、`uint32 0xffffffff+2==1`、`uint64 2^63-1+1==2^63`）成立；
6. 最后一行输出 `ALL PASS l11_const_fold`。

# 失败判读

- `const vs runtime mismatch` / `cX mismatch`：常量传播或折叠结果错误（含错误的有符号处理）；
- `negative shift/division mismatch`：算术右移被当作逻辑右移；
- `constant-sized array` / `constant grid`：常量长度/下标计算错误或 GC 后数据被破坏；
- `wrap mismatch`：运行期整数回绕语义错误。

# 参数

- 无随机路径，不读取 `UAF_SEED`；
- 常量表达式在任何优化等级下都必须得到相同值。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_const_fold/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_const_fold/*.go && /tmp/case0
```
