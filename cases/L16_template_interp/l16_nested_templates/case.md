---
id: l16_nested_templates
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/multi_test.go:97 嵌套 define/template 与变量作用域传递
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/multi_test.go:266 空 block 替换结果受模板注册顺序影响（Issue19294）
tags: [L16, templates, define, template-nesting, order-independence, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

把 `root/child/item/leaf/vars` 五个 `define` 分片按 `UAF_SEED` 决定的随机顺序 `Parse` 到同一模板集合，再用 `ExecuteTemplate` 递归渲染指针数据（`Child *child` → `Items []*item` → `Leaf *leaf`），并验证 `{{$c := .Child}}` 变量作用域与数据传递。对应 Hugo 对嵌套模板注册顺序（Issue19294）的回归测试。

# 期望行为

1. `root` 输出 `[R <C7:(a=s1)(b=s2)>|<Ltop>]`，嵌套模板按名解析且数据逐层传递正确；
2. `vars` 输出 `7:top`，局部变量与 `with` 作用域符合预期；
3. 五个分片的任意解析顺序产生相同输出；
4. 每轮 GC 后再次执行两个模板，输出不变，`Items[*].Score` 指针字段有效；
5. 最后一行 `ALL PASS l16_nested_templates`。

# 失败判读

- `FAIL: round N root/vars output`：嵌套模板数据传递或名称解析错误；若随解析顺序变化，说明依赖注册顺序；
- `FAIL: round N item score pointer ... after GC`：`[]*item` 元素指针未被保留；
- `FAIL: round N parse snippet`：分片在关联模板集合上解析失败；
- panic：模板递归/变量作用域实现缺陷。

# 参数

- `TORTURE_ROUNDS`：默认 24 轮，设为更大值做长压测（上限 200000）；
- `UAF_SEED`：决定每轮 define 分片的解析顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_nested_templates/*.go
TORTURE_ROUNDS=200 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 GODEBUG=clobberfree=1 /tmp/case
```
