---
id: l16_method_templates
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:684 `*interface` 与接口字段上的方法调用（bug1/bug2）
  - hugo@v0.155.0: common/hreflect/helpers_test.go:113 Indirect 处理双指针/接口/带方法结构体
tags: [L16, templates, methods, pointer-receiver, pointer-return, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

模板调用 `tree` 的指针接收者方法：`Label()` 读字段，`Add(d int)` 修改接收者并返回新值，`Child(i int) *tree` 带参返回指针，`Dept() *dept` 返回指针后继续取字段，`range .Kids` 对 `[]*tree` 元素调用方法。执行后 GC，再调用方法与模板，验证接收者和返回指针均有效。

# 期望行为

1. 首次渲染输出 `root#1|root-dept|a#2,b#3,|b|6|nil|up#9`；
2. `Add(5)` 将根节点 `N` 由 1 改为 6，`Child(1)` 返回同一 `*tree` 元素，`Child(9)` 返回 nil 且模板走 `else`；
3. `Dept()` 返回的 `*dept` 可继续 `.Title` 求值，`with .Parent` 沿指针链取到 `up#9`；
4. GC 后方法可继续调用，第二次渲染输出 `root#6|...|b|11|nil|up#9`；
5. 最后一行 `ALL PASS l16_method_templates`。

# 失败判读

- `FAIL: round N first/second render`：方法查找、指针接收者调用或指针返回值链式求值错误；
- `FAIL: round N method ... after GC`：方法值/接收者/返回对象在 GC 后被破坏；
- `FAIL: ... Add mutated receiver`：值/指针接收者语义错误；
- panic：nil 指针方法调用或越界索引未处理。

# 参数

- `UAF_SEED`：决定轮数（10–14），失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_method_templates/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
