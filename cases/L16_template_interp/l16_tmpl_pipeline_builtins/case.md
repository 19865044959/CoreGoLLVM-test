---
id: l16_tmpl_pipeline_builtins
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:411 call 内建调用函数字段/变参/非函数错误
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:526 slice/map index 越界与三下标切片错误
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:519 and/or 短路（未选分支不求值）
  - gitea@v1.23.5: modules/templates/util_test.go:15 变参 FuncMap dict 的合并/奇偶参数/类型检查
tags: [L16, templates, pipeline, builtins, FuncMap, dict, short-circuit, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

`text/template` 下组合自定义 FuncMap（add/sub/mul/div/wrap/joinAny/list/dict/boom/nodeName）与内建函数，覆盖管道参数顺序（管道值作为最后一个实参）、多级管道、括号化子管道、`call/index/slice/len/printf/html/js/urlquery`、变量与 `with`、`range` 双变量、dict 的 `.` 合并语义、and/or 短路以及固定错误文本。对应 Hugo 的 call/index/slice/短路测试与 gitea 的变参 dict。

# 期望行为

1. `10|sub 3` 得 -7，`"a"|wrap "<" ">"` 得 `<a>`，`2|add 3|mul 4` 得 20，`add 1 2|mul 3|sub 10` 得 1；
2. `printf "%d:%d" (add 2 3) 7` 得 `5:7`；`len/index/slice` 得 `4|2|v|[2 3]`；`call .Fn 5`/`call .Join "a" "b"` 得 `fn5|a-b`；
3. `html/js/urlquery` 分别输出 `&lt;b&gt;&amp;`、`\'` 加 `<`、`a+b%26c`；
4. `$x` 变量与 `with $m := dict` 得 `3|12`；range 双变量得 `0=x;1=y;`；逻辑组合输出 `T|T|true`；`and false (boom)`/`or true (boom)` 短路为 `false|true`，panic 函数不被调用；
5. dict 合并输出 `2|8`；`div 9 3` 得 3；变参 joinAny 得 `1,2,3`；指针函数得 `alpha:41`；
6. 错误路径均包含 `template: <name>:1:` 前缀与片段：index 越界、slice 起止倒置、call 非函数、dict 奇数参数、boom panic、除零、参数类型错误；非函数值注册 FuncMap 必须 panic；
7. 三轮 GC 后按种子顺序重放全部成功模板，输出与首次逐字节一致，数据指针/ map 有效；
8. 末行输出 `ALL PASS l16_tmpl_pipeline_builtins`。

# 失败判读

- `FAIL: pipe-order`/`vars`：管道实参顺序或作用域实现异常；
- `FAIL: escapes`：内建 html/js/urlquery 行为变化；
- `FAIL: short-circuit`：and/or 未短路导致 boom 被调用；
- `FAIL: ... missing name/line`：错误包装丢失模板名/行号；
- GC 轮次失败：模板/FuncMap/数据指针跨 GC 失效。

# 参数

- `UAF_SEED`：决定 GC 轮次模板重放顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_tmpl_pipeline_builtins/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
