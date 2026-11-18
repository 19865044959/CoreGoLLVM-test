---
id: l13_utf8_process
dim: L13_runtime_ds
priority: P1
sources:
  - sonic@v1.13.2: utf8/utf8_test.go:97 TestValidate_Random 随机字节串的 UTF-8 合法性校验（与 stdlib 对照）
  - goccy-json@v0.10.5: stream_test.go:529 TestIssue278 长字符串末尾 \u 转义、含中文键的流式解码
tags: [L13, utf8, rune, invalid-sequence, map-key, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

UTF-8 处理与多字节 map key 的跨 GC 校验：

1. 固定语料覆盖 ASCII、预组合/组合音标、中文、emoji、U+FFFD、NUL，以及非法序列（孤立 0x80/0xFF、截断的 2/3 字节序列、代理区、超出 U+10FFFF、overlong、5 字节序列）；
2. 对每个语料对照 `utf8.Valid/ValidString`、`utf8.RuneCountInString` 与手写 `DecodeRuneInString` 统计（size==1 的 RuneError 计为非法）；
3. 合法串在每个 rune 边界截断必须仍合法且等于对应 rune 前缀；非法串在任意字节截断满足 `Valid == (非法 rune 数==0)`；多字节字符中间截断必须非法；
4. 合法串与非法串分别作为 `map[string]*uentry` 的 key，GC 后用新建 []byte 重建 key 查询，校验 rune/字节计数；
5. 16KB 大字符串 `"aé你🙂"×250` 构建 `map[rune]int`，GC 后按固定 rune 列表校验各计数 250、总 rune 1000、字节长度 2500；
6. `UAF_SEED` 驱动的 2000 个随机样本（1000 个任意字节串 + 1000 个随机合法 rune 串），逐样本对照 Valid/RuneCount/roundtrip，每 17 个样本做全部字节截断性质检查，每 256 个样本显式 GC + 垃圾分配；最终按排序 key 校验随机合法串 map。

# 期望行为

1. 固定语料的 valid/runes/invalid 三元组全部匹配（含 U+FFFD 合法单 rune、代理区与超范围序列全部非法）；
2. 合法串的所有 rune 边界前缀合法且内容正确；非法串所有字节截断的合法性与非法 rune 数一致；
3. 合法/非法 map key 在 GC 后仍可查，计数字段正确；
4. 大字符串 rune 计数 map 在 GC 后 4 个 rune 各 250 次；
5. 2000 个随机样本满足 `ValidString(s) == (invalid==0)`、RuneCount 一致、合法样本 rune roundtrip 一致；
6. 随机合法串 map 在最终 GC 后内容与计数正确；
7. 最后一行 `ALL PASS l13_utf8_process`。

# 失败判读

- 固定语料的 valid/rune 数不符：UTF-8 校验/解码路径错误（注意 RuneError 的 size 区分）；
- rune 边界截断出现非法串：边界推进（DecodeRune 的 size）错误；
- map key GC 后查不到：string 数据未被扫描/保活（UAF）；
- 大字符串计数错误：range 解码或 map[rune] 统计错误；
- 随机样本性质不满足：编译器/runtime 对 utf8 相关代码的优化或边界检查问题。

# 参数

- `UAF_SEED`：随机样本选择的种子，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_utf8_process/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
