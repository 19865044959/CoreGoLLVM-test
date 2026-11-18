---
id: l16_nested_method_block
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/multi_test.go:97 嵌套 define/template 与变量作用域传递
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/multi_test.go:266 block 替换结果与模板注册顺序（Issue19294）
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:684 `*interface`/接口字段上的方法调用（bug1）
  - hugo@v0.155.0: common/hreflect/helpers_test.go:113 Indirect 处理双指针/接口/带方法结构体
  - yaegi@v0.16.1: interp/interp_eval_test.go:580 方法集：值/指针接收者、嵌入与接口断言
tags: [L16, templates, nested-template, block, methods, pointer-receiver, method-error, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

`layout/head/body/row` 四个 `define` 以随机顺序注册，`layout` 内用 `{{block "footer"}}DEFAULT{{end}}` 定义默认页脚，再由后注册的 `define "footer"` 覆盖；另建一组“覆盖先注册、layout 后注册”的模板验证 block 默认值胜出。模板在嵌套 `template` 调用中访问指针接收者方法 `Label/Child/Find`、返回 `(*item, error)` 的 `FindErr` 与返回 `(string, error)` 的 `Boom`，并调用会修改接收者的 `Scale`。对应 Hugo 的嵌套 define/block 顺序回归测试。

# 期望行为

1. 覆盖页脚模板输出 `<T/r#1|0:r#1>a#2@0/p:r#1;1:r#1>b#4@1/p:r#1;|X[r#1/a1#3/b#4/none]>`，嵌套行模板拿到正确的 root/kid/parent 指针；
2. 先注册覆盖再解析 layout 时 block 默认页脚胜出，输出以 `|DEFAULT>` 结尾；
3. `Boom` 返回错误使 Execute 失败，错误含 `error calling Boom: boom-method`，错误点之后的 `post` 不输出；
4. `FindErr` 的错误经模板执行返回，含 `find-failed: x`；
5. `Scale` 连续执行输出 `3|m#3`、`9|m#9`，GC 后第三次为 `27|m#27`，同一接收者被修改；
6. 每轮 GC 后 parent/kid 指针链、`Child`/`Find` 方法与嵌套模板输出不变，错误文本逐字节稳定；
7. 最后一行 `ALL PASS l16_nested_method_block`。

# 失败判读

- `layout` 输出差异：嵌套模板数据传递、`block` 覆盖顺序或方法解析错误；
- `Boom/FindErr returned nil error`：方法错误被模板引擎吞掉；
- `wrote past error`：错误后仍执行后续动作；
- GC 后 parent/Child/Find 失败：指针链或方法接收者被破坏；
- `Scale after GC` 非 27：方法值/接收者状态丢失。

# 参数

- `TORTURE_ROUNDS`：默认 20 轮，可放大（上限 200000）；
- `UAF_SEED`：决定 define 注册顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_nested_method_block/*.go
TORTURE_ROUNDS=500 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 GODEBUG=clobberfree=1 /tmp/case
```
