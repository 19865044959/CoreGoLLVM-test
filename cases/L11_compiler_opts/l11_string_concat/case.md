---
id: l11_string_concat
dim: L11_compiler_opts
priority: P2
sources:
  - go-redis@v9.8.0: internal/proto/reader_test.go:71 8KB 单行只在末尾遇 CRLF，循环 ReadSlice 拼接
  - goccy-json@v0.10.5: json_test.go:197 TestCompactBig 10000 元素随机结构 JSON 压缩，字节级往返
tags: [string-concat, strings.Builder, byte-append, rune-count, unicode, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 12 轮，约 4096–6143 个片段；TORTURE_ROUNDS=100 时 1200 轮）"
---

# 场景

模拟协议行拼接与 JSON 编码缓冲：每轮生成 4096–6143 个片段，内容混合十进制整数、ASCII、`世界`、`-NN`，分别用 `s += p`、`strings.Builder`（预 `Grow`）、`[]byte` append（预分配）三种方式拼接。校验三种结果逐字节相同、字节长度、rune 数、Unicode 片段计数、前缀/后缀与第 1000 个片段的字节偏移切片。每轮做垃圾分配 + 两次 `runtime.GC()`，保留全部结果字符串，最后统一复核长度与 rune 数。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 三种拼接方式产出的字符串完全相等；
2. 字节长度等于各片段 `len` 之和，rune 数等于 `utf8.RuneCountInString` 之和；
3. `strings.Count(s, "世界") == (n+2)/5`，前缀/后缀与原文一致；
4. 第 1000 个片段在计算出的字节偏移处逐字节相等；
5. GC 后所有保留字符串的长度与 rune 数不变，且仍包含 `世界`；
6. 默认轮数 12，实际轮数为 `12 * min(TORTURE_ROUNDS,100)`；
7. 最后一行输出 `ALL PASS l11_string_concat`。

# 失败判读

- `plus vs builder mismatch` / `builder vs bytes mismatch`：拼接路径存在漏字节/重复/越界写；
- `byte length mismatch` / `rune count mismatch`：UTF-8 编码或截断错误；
- `interior byte offset mismatch`：Buffer/Grow 容量计算或拼接顺序错误；
- `kept string ... corrupted after GC`：字符串底层数组被回收或复用；
- 失败输出带 `UAF_SEED`，并显示轮数相关的保留长度。

# 参数

- `UAF_SEED`：决定基数 `4096 + seed%2048`，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（即 12 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_string_concat/*.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=50 UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_string_concat/*.go && UAF_SEED=7 /tmp/case0
```
