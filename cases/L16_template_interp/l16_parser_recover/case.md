---
id: l16_parser_recover
dim: L16_template_interp
priority: P0
sources:
  - yaegi@v0.16.1: interp/interp_eval_test.go:1426 REPL 逐行累积：括号/多行字符串/逗号未闭合时不报错，补齐后继续解析且错误行号准确
  - yaegi@v0.16.1: interp/interp_file_test.go:26 ParseComments 提取源码尾部 Output:/Error: 注释作为 golden
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 ParseFile(ParseComments) 驱动源码诊断
tags: [L16, goast, go-parser, error-recovery, error-list, incremental-parse, comments, GC]
requires: [goast]
runtime: "<3s"
---

# 场景

模拟 REPL/代码修复工具对不完整源码的处理：`parser.ParseFile(AllErrors)` 在语法错误下仍返回部分 AST，`scanner.ErrorList` 按位置有序；`parser.ParseExpr` 对未闭合的括号/字符串/复合字面量报错，补齐后可继续解析并用 `go/printer` 打印确定文本。另用 `ParseComments` 读取源码尾部 `// Output:` / `// Error:` 约定，对应 yaegi 的 REPL 续行与文件测试 golden 提取。

# 期望行为

1. 合法源码带 2 个注释组，尾部注释解析出 `Output: 42`、`Error: none`，顶层声明 1 个；
2. 坏源码返回非 nil 的部分 AST（包名 `bad`、保留 `broken()`），ErrorList 至少 3 条且按 offset 非降序，全部带行号并含一条 `expected`/`missing` 消息；`broken` 位置指向 `bad.go` 第 3 行及以后；
3. 6 组不完整表达式先失败，补齐后解析成功且打印结果与预期逐字节一致（`f(1, 2)`、`[]int{1, 2, 3}`、map 字面量等）；5 组非法表达式必须失败；
4. 修复后的源码零解析错误、2 个函数、1 个注释组；
5. 三轮 GC 后重新解析坏源码，ErrorList 条数不变、部分 AST 仍含 `broken()`，保留的合法 AST 遍历节点数 >= 20；
6. 末行输出 `ALL PASS l16_parser_recover`。

# 失败判读

- `FAIL: bad source must yield partial AST` / ErrorList 不排序：解析器错误恢复或位置处理异常；
- `FAIL: expr ... printed`：增量解析 AST 与 printer 输出不一致；
- `FAIL: round N ...`：AST/位置在 GC 后损坏（stack map 或对象存活问题）；
- panic：解析器内部崩溃，记录 UAF_SEED 复现。

# 参数

- `UAF_SEED`：决定每轮表达式解析顺序，失败输出带该种子（默认 1）。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_parser_recover/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
