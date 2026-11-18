---
id: l16_vars_expand
dim: L16_template_interp
priority: P0
sources:
  - gitea@v1.23.5: modules/templates/vars/vars_test.go:12 `{a}` 单变量展开与多变量替换
  - gitea@v1.23.5: modules/templates/vars/vars_test.go:36 中文变量名 `{一}` 等 Unicode 展开
  - gitea@v1.23.5: modules/templates/vars/vars_test.go:45 `{{a}` 部分展开并报错、`{ }`/`{#}` 保持原文
  - gitea@v1.23.5: modules/templates/vars/vars_test.go:54 孤立 `}`/`{` 原样输出并报错
tags: [L16, scanner, brace-expand, unicode, error-position, determinism, GC]
requires: []
runtime: "<3s"
---

# 场景

实现 gitea `vars` 风格的 `{name}` 变量展开器：单遍字节扫描，合法名字按 map 替换，非法名字（空、空格、`#`）原样保留且不报错；未定义变量、双花括号、孤立 `{`/`}` 记录带字节位置的首个错误并尽量继续展开后续内容。覆盖 ASCII/中文、相邻占位符、二次展开、20000 占位符批量输入，以及种子驱动的随机模板确定性。

# 期望行为

1. `{a}`→`1`；混合文本 `{a},{b},{c}` 与非法 `{ }`/`{#}` 保持原文且无错误；中文变量名正常替换；
2. `{{a}` 记录 `nested brace`；`expand } {} and {` 记录 `unmatched` 与 `unclosed`；`{missing}` 记录 `undefined variable`；`{a` 记录 `unclosed`；`a {b c} d` 无错误；
3. 同一错误输入重复展开，错误文本逐字节一致；20000 个 `{a}` 全部替换、输出长度 20000；
4. 3 轮共 900 个随机模板：两次展开输出与错误确定性一致，无 panic；
5. 300 个只含已定义合法占位符的随机模板与独立字符串替换参考结果完全一致；
6. 值本身含 `{b}` 时二次展开得到最终值 `2`；
7. 三轮 GC 后固定用例与保留输出不变，`vars` map 内容不变；
8. 末行输出 `ALL PASS l16_vars_expand`。

# 失败判读

- `FAIL: case N output`：展开器状态机在合法/非法名字边界处理错误；
- `FAIL: ... err=... want ...`：未定义/未闭合/嵌套错误分类或位置错误；
- `FAIL: ... nondeterministic`：随机输入下输出依赖 map 遍历或全局状态；
- `FAIL: reference mismatch`：合法占位符替换语义偏差；
- GC 轮次失败：保留字符串或变量表被破坏。

# 参数

- `UAF_SEED`：随机模板生成与顺序种子，失败输出带该种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_vars_expand/*.go
UAF_SEED=9 GODEBUG=clobberfree=1 /tmp/case
```
