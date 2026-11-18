---
id: l11_string_concat_escape
dim: L11_compiler_opts
priority: P2
sources:
  - fasthttp@v1.59.0: header_test.go:2448 getHeaders 用 strings.Join 拼接 header 行
  - fasthttp@v1.59.0: http_timing_test.go:69 copyZeroAlloc 写入 strings.Builder 并 Reset 复用缓冲
  - goccy-json@v0.10.5: json_test.go:197 TestCompactBig 大结构 JSON 的字节级往返
tags: [string-concat, strings-builder, escape-analysis, substring-aliasing, invalid-utf8, map-key, interface-boxing, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4 轮；TORTURE_ROUNDS=100 时 400 轮）"
---

# 场景

模拟 header 拼接、零分配 Builder 复用与 JSON 压缩中的字符串拼接链逃逸：每轮把 120..199 个混合 ASCII/Unicode/NUL/非法 UTF-8 片段用多操作数 `+` 链拼成长串，与 `strings.Builder` 对照；把结果存入 `[]any`、struct 的 `any` 字段、map 键与切片，显式 GC 后复核。Builder 取串后继续写入、Reset 后再写，验证旧字符串不被后续复用破坏。

# 期望行为

1. `+` 链与 Builder 得到完全相同的字符串，字节长度等于各片段长度之和；
2. 取出的 `s1` 在 Builder 继续追加后保持不变，追加得到 `s2 == s1+"TAIL"`；
3. Builder `Reset` 后重用不改变已取出的 `s1`/`s2`；
4. 子串拼接的前缀/后缀与长度正确；五个操作数的链结果正确；
5. 非法 UTF-8 与 NUL 字节经拼接后字节精确（长度和内容相等）；
6. 拼接构造的 map 键在 GC 后仍可命中；
7. `[]any`、struct `any` 字段、`[]string` 中保留的拼接结果在最终 GC 后逐项等于期望；
8. 所有轮次的结果都确定，重跑输出一致；
9. 最后一行输出 `ALL PASS l11_string_concat_escape`。

# 失败判读

- `concat chain vs builder`：拼接链被错误折叠或丢掉片段；
- `builder string stable after append` / `builder reset preserves old strings`：Builder 缓冲区复用破坏了已取出的字符串视图；
- `substring chain`：子串拼接长度/首尾错误；
- `invalid utf8 concatenation`：拼接对非法 UTF-8 字节做了替换或截断；
- `retained ... after GC`：逃逸字符串被回收或内容损坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮片段数 120..199 与片段内容，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（4 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_string_concat_escape/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_string_concat_escape/*.go
UAF_SEED=1 /tmp/case_n
```
