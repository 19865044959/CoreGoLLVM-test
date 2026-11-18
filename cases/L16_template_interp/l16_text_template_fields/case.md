---
id: l16_text_template_fields
dim: L16_template_interp
priority: P0
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:299 模板沿 `.U.V` 求值连续两层指针字段
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:359 `any` 中 struct/slice 嵌套字段递归取值
  - gitea@v1.23.5: modules/templates/helper_test.go:68 模板真值矩阵覆盖各级指针
tags: [L16, templates, text-template, pointer-fields, GC]
requires: [templates]
runtime: "<3s"
---

# 场景

用 `text/template` 渲染含多层指针字段的 `user`（`Addr *address`、`Score *int`、`Friend *user`），每次渲染后分配垃圾并执行两次 `runtime.GC()`，再次渲染并逐字节比较输出。对应 Hugo 内嵌 text/template 对 `.U.V` 指针链与嵌套字段的求值路径。

# 期望行为

1. 模板输出固定为 `u<r>|Beijing|100<r>|10+r|f|Shanghai|a,b,|nil`，指针字段打印其间接值，nil 指针在 `if` 中为假；
2. `runtime.GC()` 后 `Addr/Score/Friend` 指针及其指向的字段保持不变；
3. GC 后第二次渲染与第一次输出完全一致；
4. 最后一行输出 `ALL PASS l16_text_template_fields`，退出码 0。

# 失败判读

- `FAIL: round N score/addr/friend pointer ... after GC`：模板持有的数据指针在 GC 后被清扫或字段被破坏；
- `FAIL: round N first/second render`：字段求值/指针间接化结果不正确；
- `SIGSEGV` / `fatal error: found bad pointer`：栈或堆上的指针未被正确标记；
- 输出中出现十六进制地址：模板未对指针做间接化，属于语义错误。

# 参数

- `UAF_SEED`：调整默认轮数（16–24 轮），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_text_template_fields/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
