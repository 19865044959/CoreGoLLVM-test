---
id: l16_types_scope_generic
dim: L16_template_interp
priority: P1
sources:
  - tailscale@v1.80.2: cmd/viewer/viewer_test.go:41 parser.ParseFile + types.Info 驱动类型分析
  - tailscale@v1.80.2: cmd/viewer/viewer_test.go:59 pkg.Scope().Lookup 取 TypeName 并检查 Named
  - tailscale@v1.80.2: util/codegen/codegen_test.go:204 types.TypeParamList 与参数化类型处理
  - golangci-lint@v2.0.2: pkg/golinters/nolintlint/internal/nolintlint_test.go:281 单文件类型检查路径
tags: [L16, goast, go-types, generics, type-params, instances, scopes, methodsets, GC]
requires: [goast]
runtime: "<10s"
---

# 场景

模拟 tailscale codegen / golangci-lint 对泛型源码的构建期分析：包作用域里是泛型 Named
类型与泛型函数，函数作用域同时包含类型参数、普通参数与局部变量，方法还有独立的接收者
类型参数。检查 `TypeParams/RecvTypeParams`、`Info.Instances`、实例化后的方法集与
type-switch 隐式变量 case 作用域，并在多轮 GC 后复核 `types.Object` 身份。

## 期望行为

1. 包作用域恰为 `[Box Map Number Sum Use]`，类型参数 `T` 不泄漏到包级；
2. `Box[T any]` 的 `TypeParams` 长度 1、约束为 any、方法集含 `Get`，接收者类型参数长度 1；
3. 方法作用域为 `[T b]`，其中 `T` 与 `RecvTypeParams().At(0).Obj()` 是同一对象；
4. 泛型函数作用域为 `[T U f out xs]`，range 变量不在函数级作用域，`LookupParent(T)` 命中函数作用域；
5. `Sum[T Number]` 约束为 `demo.Number`；
6. `Info.Instances` 中 `Box` 的 TypeArgs 为 `int`、实例类型 `demo.Box[int]`；`Map` 为两个 `int`；
7. `Instantiate(Box, {int})` 成功且 `*Box[int]` 方法集 `Get` 的签名为 `func() int`；类型实参数量错误时返回错误；
8. `b.Get()` 选择器为 `MethodVal`，接收者 `demo.Box[int]`；
9. type-switch 隐式变量 `v` 位于 case 作用域，case 内使用解析到同一对象；
10. 3 轮 GC 后包对象、类型参数、方法对象、函数作用域对象身份不变（种子打乱复核顺序）。

## 失败判读

- `scope=...` / `TypeArgs=...`：go/types 泛型作用域或实例化信息错误；
- `object changed`：类型对象在 GC 后失效，属于对象存活问题；
- `Instantiate ...`：泛型实例化路径异常或与 Go 1.24 语义不符；
- 超时：类型检查或方法集计算出现死循环。

## 参数

- `UAF_SEED`：控制每轮 GC 后包级名字复核顺序；失败输出带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_types_scope_generic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
