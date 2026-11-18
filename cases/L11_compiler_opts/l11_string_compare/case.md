---
id: l11_string_compare
dim: L11_compiler_opts
priority: P2
sources:
  - echo@v4.13.4: bind_test.go:741 按 Content-Type 字符串前缀链选择绑定器
  - gorm@v1.30.0: clause/set_test.go:53 strings.Compare 对赋值列名排序
  - fasthttp@v1.59.0: uri_test.go:107 TestURIUpdate 请求 URI 的精确字符串比较
tags: [string-compare, memequal, lexicographic, sort, map-key, unicode, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 6 轮 × 约 49 个字符串；TORTURE_ROUNDS=100 时 600 轮）"
---

# 场景

模拟路由/内容类型判断与 SQL 构造中的字符串比较：固定语料包含空串、单字节、长公共前缀（4096 字节）、前缀串、尾部差异、含 `\x00` 字节、Unicode `世界`、数字/字母边界与相似 ID；每轮再按 `UAF_SEED` 生成 24 个随机长度/字节的字符串（可能含非法 UTF-8，比较仍按字节）。对语料做全对全的 `==`、`<`、`>` 与逐字节参考比较、`strings.Compare` 对照；再用 `<` 排序并校验顺序与多重集不变；构建 map 键做精确查找；校验 `string([]byte(s))` 与自切片相等。全部语料在 GC 后再次比较。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 每一对的 `==`、`<`、`>` 与逐字节字典序参考完全一致；
2. `strings.Compare` 返回符号与参考一致；
3. 排序后相邻元素满足参考 `<=`，且多重集与输入一致；
4. map 键查找对全部语料命中，值为最后一次写入下标；
5. `string([]byte(s)) == s`、`s[:len(s)] == s`；
6. GC 后抽样比较与转换仍正确；
7. 默认 6 轮，实际轮数为 `6 * min(TORTURE_ROUNDS,100)`；
8. 最后一行输出 `ALL PASS l11_string_compare`。

# 失败判读

- `string equality` / `string less-than` / `string greater-than`：长度检查、公共前缀跳过或尾部比较的错误优化；
- `strings.Compare reference`：运行期 cmpstring 与语言比较语义不一致；
- `sorted order` / `sort permutation`：比较结果不可传递或排序破坏元素；
- `map key lookup`：字符串哈希/相等判定错误；
- `string conversion equality` / `kept string after GC`：字符串底层数据在 GC 后被破坏；
- 失败输出带 `UAF_SEED`；随机串不打印，仅报检查项。

# 参数

- `UAF_SEED`：决定随机语料与轮次派生值，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（6 轮），上限 100。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_string_compare/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=5 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_string_compare/*.go
UAF_SEED=1 /tmp/case_n
```
