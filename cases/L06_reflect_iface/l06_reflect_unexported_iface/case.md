---
id: l06_reflect_unexported_iface
dim: L06_reflect_iface
priority: P1
sources:
  - gorm@v1.30.0: schema/field_test.go:242 字段权限 tag 反射元数据与只读判定
  - tailscale@v1.80.2: ipn/ipnlocal/bus_test.go:22 reflect 枚举全部 struct 字段
  - caddy@v2.10.0: context_test.go:22 struct tag 反射定位字段 + 接口断言
tags: [L06, reflect, unexported-field, embedded-struct, interface-field, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

反射读取包内未导出字段（含匿名嵌入未导出 struct、嵌套未导出字段、带 tag 的未导出字段），
并通过 `FieldByName` 读取导出字段、接口字段与 `map[string]*node` 的指针元素。未导出字段只能
用 `Int/String/Float` 等 Kind 读取，`Interface/Set` 必须 panic；导出字段可写。每轮写入后显式
GC，再次从全局接口槽回读，验证字段偏移、接口数据区与嵌套指针在 GC 后仍正确。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l06_reflect_unexported_iface`；
2. `Type.NumField/Field(i)/FieldByName` 正确识别匿名嵌入与未导出字段，tag 可读；
3. 未导出字段 `CanInterface/CanSet` 均为 false，调用 `Interface()` 或 `SetInt()` 必须 panic 且被 recover；
4. 导出嵌套字段可写，接口字段的方法可经 `reflect.Value.MethodByName` 与 `Interface()` 调用；
5. 每轮显式 `runtime.GC()` 后，未导出值与接口字段中的 `*node` 数据不变；
6. base/clobberfree/efence/stress/k8s 下结果一致。

## 失败判读

- `FAIL: Interface on unexported field must panic`：reflect 未正确携带只读 flag；
- 未导出字段 Int/String 值错误：struct 字段偏移或名字解析错误；
- 接口字段方法调用 panic：接口 Value 的方法集或 data 指针被破坏；
- GC 后 `any`/接口字段内容丢失或崩溃：接口数据区/嵌入字段指针未被扫描；
- `SIGSEGV`/`found bad pointer`：指向编译器 GC 元数据或栈映射问题。

## 参数

- `UAF_SEED`：随机种子，控制字段取值、map 插入顺序与遍历顺序；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_unexported_iface/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
