---
id: l12_const_methodset
dim: L12_init_link
priority: P0
sources:
  - caddy@v2.10.0: modules/caddytls/matchers_test.go:293 type testAddr string 及 String/Network 方法
  - gitea@v1.23.5: modules/translation/i18n/i18n_test.go:90 指针接收者 Stringer
  - gitea@v1.23.5: modules/translation/i18n/i18n_test.go:98 值接收者 Stringer
tags: [L12, constants, iota, method-set, value-receiver, pointer-receiver, interface, itab, gc]
requires: []
runtime: "<3s"
---

# 场景

定义 `level`、`label`、`flags` 三个具名常量类型并挂载方法：String/Weight 为值接收者，Promote 为指针接收者。包级编译期断言覆盖 `stringer`、`weighter`（嵌入 stringer）、`upper`、`fmt.Stringer` 与 `promoter`；包级接口切片/接口变量由类型化常量初始化。主程序验证常量方法调用、方法表达式/方法值、接口相等、type switch、typed-interface 断言与指针接收者规则。

# 期望行为

1. `levelDebug..levelError = 0..3`；`const promoted = levelWarn+1` 仍为 `level` 类型且等于 `levelError`；
2. `levelWarn.String()=="warn"`、`levelError.Weight()==30`、`labelAPI.Upper()=="API"`、`labelDB.Kind()=="label"`；
3. `flagAll` 含三个位，`Count()==3`，`Has` 对组合位成立，对未置位不成立；
4. `fmt.Sprint` 分别输出 `warn`/`db`/`flags(7)`，走 Stringer 方法集；
5. 嵌入接口 `weighter` 同时派发 `Weight` 与 `String`；
6. 方法表达式 `level.Weight`、`label.Upper` 与方法值 `levelError.String` 结果正确；
7. `level` 值不实现 `promoter`（指针接收者不在值方法集），`*level` 实现且 `Promote` 修改原变量；
8. 包级 `[]fmt.Stringer` 的 7 个常量来源按 seed 轮换派发均正确；接口相等规则成立；
9. type switch 对 level/label/flags 的计数为 2/1/1；
10. 8 goroutine 只读接口 + GC 后派发结果不变；最后一行 `ALL PASS l12_const_methodset`。

# 失败判读

- 编译失败 `does not implement`：常量类型的方法集/接口嵌入实现错误；
- `level` 值断言 `promoter` 意外成功：指针接收者方法集规则错误；
- 常量运算类型丢失或值错误：常量折叠/类型传播错误；
- `fmt.Sprint` 输出不是方法结果：itab/接口派发错误；
- GC 后接口派发结果变化：接口数据字或 itab 扫描错误。

# 参数

- `UAF_SEED`：决定包级 Stringer 切片的轮换起点；断言与起点无关。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_const_methodset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
