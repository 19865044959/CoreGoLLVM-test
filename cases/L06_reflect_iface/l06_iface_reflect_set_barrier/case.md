---
id: l06_iface_reflect_set_barrier
dim: L06_reflect_iface
priority: P0
sources:
  - gorm@v1.30.0: schema/field_test.go:155 反射把 sql.Scanner/Valuer 具体值 Set 进模型字段
  - echo@v4.13.4: bind_test.go:336 通过反射探测并写入 TextUnmarshaler 接口实现
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 接口字段的方法经 wrapper 动态派发
tags: [L06, reflect, interface-field, Set, write-barrier, dynamic-type, GC]
requires: [reflect]
runtime: "<3s"
---

# 场景

框架把解码结果写进 `any`/`fmt.Stringer`/自定义接口字段时，接口的 type 字与 data 字会被整体替换。
本用例把 `holder` 先 GC 进老年代，再用 `reflect.Value.Set` 反复向三个接口字段写入不同动态类型
的具体指针（`*payload`、`*circle`）、字符串与 nil，并丢弃所有局部根；每个写入点后显式 GC，验证
old→new 写屏障、接口数据区扫描、动态类型断言与反射方法派发。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_iface_reflect_set_barrier`；
2. `any` 字段中的 `*payload` 及其 `Tag *node` 在多次显式 GC 后仍可解引用，标量保持写入值；
3. `fmt.Stringer` 字段中的动态类型可继续调用 `String()`，反射 `MethodByName("Shape")` 与直接
   接口调用结果一致；
4. 同一 `any` 槽在 `*payload`→`string`→`*payload`→nil→`*payload` 之间切换后，动态类型与 nil
   判定始终正确；独立 `reflect.New(interface{}).Elem()` 槽仅靠自身保活指针；
5. 不满足接口的 `int` 写入 `fmt.Stringer` 字段必须 panic 并被 recover；
6. `holder` 只由 `any`/局部接口槽保活时，GC 后所有接口字段、指针字段与标量仍然一致。

# 失败判读

- `... after GC`：接口字段写入的新指针未被写屏障记录，对象被提前回收；
- `non-implementer ... panics` 缺失：反射接口赋值检查失败；
- `reflect method on interface field` / `reflect interface field method`：接口字段的反射方法派发错误；
- `any slot ... dynamic type`：接口 type/data 字替换错误；
- SIGSEGV / `found bad pointer`：接口数据区按错误类型元数据扫描。

# 参数

- `UAF_SEED`：每轮写入的基值与标签值来源；失败行带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_reflect_set_barrier/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
