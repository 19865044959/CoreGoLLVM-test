---
id: l11_string_concat_chain
dim: L11_compiler_opts
priority: P1
sources:
  - fasthttp@v1.59.0: header_test.go:881 循环中 b += "foobar" 拼接 header 片段
  - echo@v4.13.4: bind_test.go:698 path += "?" + query.Encode() 构造请求路径
  - go-redis@v9.8.0: internal/proto/reader_test.go:71 8KB 单行循环 ReadSlice 后拼接
tags: [string-concat, constant-fold, strings.Builder, strings.Join, unicode, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 HTTP 头/请求路径与 RESP 大行拼接：覆盖 5 操作数 `+` 链的求值顺序、2/3 操作数链、空操作数、常量串折叠、循环 `+=` 与 `strings.Builder`/`strings.Join` 的逐字节等价、子串拼接独立、rune/byte 转换后拼接，以及 256..383 个混合 ASCII/Unicode 片段的累计拼接。所有结果保留到显式 GC 后复核长度、rune 数与内容。

# 期望行为

1. 5 操作数链得到 `7-b-c-d-e`，函数调用顺序为 1..5；
2. 空串与两端/中间空操作数拼接结果不变；
3. 常量 `"ab"+"cd"+"ef"` 折叠为 6 字节，运行期分步拼接相等；
4. 循环 `+=`、Builder、Join 三者逐字节相等；
5. 字节长度与 rune 数分别等于各片段之和，`世界` 出现次数符合 `(n+1)/4`；
6. 子串拼接内容正确，原字符串不变；
7. rune 与 byte 转字符串后的拼接结果一致；
8. GC 后链、长串、常量串、子串结果的长度与内容不变；
9. 最后一行输出 `ALL PASS l11_string_concat_chain`。

# 失败判读

- `five operand concatenation` / `evaluation order`：多操作数拼接漏字节/重复或求值顺序改变；
- `constant concatenation`：常量折叠结果错误；
- `loop plus vs Builder` / `loop plus vs Join`：循环拼接路径不一致；
- `byte length` / `rune count` / `unicode`：UTF-8 编码或截断错误；
- `substring concatenation` / `base string unchanged`：拼接错误复用了子串底层数据；
- `... after GC`：字符串底层数组被回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定片段数 256..383，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_string_concat_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_string_concat_chain/*.go
UAF_SEED=1 /tmp/case_n
```
