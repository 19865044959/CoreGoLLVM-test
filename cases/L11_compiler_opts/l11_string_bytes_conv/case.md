---
id: l11_string_bytes_conv
dim: L11_compiler_opts
priority: P1
sources:
  - goccy-json@v0.10.5: decode_test.go:1955 TestLargeByteSlice 2000 字节 []byte marshal/unmarshal 往返
  - goccy-json@v0.10.5: json_test.go:197 TestCompactBig 10000 元素随机结构 JSON 字节级往返
  - fasthttp@v1.59.0: uri_test.go:468 Parse 不得原地改写调用方输入 buffer
tags: [string-bytes-conversion, copy-semantics, unicode, invalid-utf8, map-key, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 JSON 压缩与 URI 解析中的 `string`/`[]byte` 转换：每轮按 `UAF_SEED` 生成长度 257..383 的字节串（含 `0xff/0xfe/0x00`），做 `string(b)`、`[]byte(s)`、`append(nil, s...)`、子切片修改、map 键查找与 `strings.Clone`；转换后分别修改源字节与转换结果，验证字符串内容不变。另测 nil/空值、Unicode 字节与 rune 计数、rune→string 边界（-1、0x110000、0）、运行期整数转字符串、非法 UTF-8 原样保留。保留字符串跨最终 GC 后校验长度与校验和。

# 期望行为

1. `string(b)` 与源字节逐字节一致，长度为字节长度；
2. 转换后修改源字节，已保存字符串的内容与校验和不变；
3. 修改 `[]byte(s)` 及其子切片，字符串内容不变；
4. `append([]byte(nil), s...)` 与 `[]byte(s)` 内容一致且独立；
5. 字符串作为 map 键保留创建时内容，源字节改动后仍能命中，改动后的源字节不命中；
6. nil/空 bytes 与空字符串互相转换长度为 0；
7. `"世界abc"` 字节长 9、rune 数 5，round-trip 一致；`string([]rune{'世','界'})=="世界"`；
8. `string(rune(-1))` 与 `string(rune(0x110000))` 均为替换字符；`string(66)=="B"`；
9. 非法 UTF-8 字节序列转换后字节长度与内容不变；
10. 保留字符串在最终 GC 后长度与校验和不变；
11. 最后一行输出 `ALL PASS l11_string_bytes_conv`。

# 失败判读

- `string changed after ...`：转换未复制，发生别名写；
- `map key lookup ...`：字符串键随源 buffer 改动而变化；
- `unicode ...` / `rune ... conversion`：Unicode 或 rune 转换语义错误；
- `invalid UTF-8 ...`：转换过程校验/改写了非法字节；
- `kept string ... after GC`：字符串底层数据被回收/破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮长度、内容 salt 与轮间变化，失败输出带种子；
- 本用例为 P1，不支持 `TORTURE_ROUNDS`。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_string_bytes_conv/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_string_bytes_conv/*.go
UAF_SEED=1 /tmp/case_n
```
