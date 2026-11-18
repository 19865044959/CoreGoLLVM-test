---
id: l16_funcmap_pointer
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:695 函数实参指针/值间接化（bug6a–d）
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:1796 call 参数个数与可赋值性检查
  - gitea@v1.23.5: modules/templates/util_test.go:15 变参 FuncMap（dict 合并/类型检查）
tags: [L16, templates, FuncMap, pointers, variadic, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

注册一组 `FuncMap`：`newItem` 返回 `*item`，`label/score/bump/link/pick` 接收指针，`countPtrs` 为 `...*item` 变参。模板内串联调用这些函数，随后在 Go 侧直接调用同一批函数并保留返回的指针链，GC 后校验字段与 `Next` 指针，再执行同一模板。

# 期望行为

1. 模板输出 `1/a;2/b;34/c+d;<score>;<id>/c;2`，`with bump ...` 对返回指针取字段正确；
2. `bump(a).Next == a`、`link(a,b).Next == b`、`pick(a,b) == a`；
3. 变参 `countPtrs(a,b,picked) == 3`，nil 指针参数返回哨兵值而不 panic；
4. GC 后返回指针的字段和 `Next` 链保持有效，模板输出不变；
5. 最后一行 `ALL PASS l16_funcmap_pointer`。

# 失败判读

- `FAIL: round N bump/link/picked ... after GC`：FuncMap 返回的堆指针未被正确保留；
- `FAIL: round N output`：模板对指针实参的间接化/可变参数打包错误；
- `FAIL: ... variadic pointer count`：变参 `...*T` 参数打包数量错误；
- panic：nil 指针被解引用。

# 参数

- `UAF_SEED`：决定轮数（12–18），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_funcmap_pointer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
