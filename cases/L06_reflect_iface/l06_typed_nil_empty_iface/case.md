---
id: l06_typed_nil_empty_iface
dim: L06_reflect_iface
priority: P0
sources:
  - caddy@v2.10.0: modules/caddyhttp/reverseproxy/upstreams_test.go:5 *bool 三态 nil 归一化
  - gin@v1.10.0: context_test.go:2422 零值 map/接口的惰性初始化
  - fiber@v2.52.6: ctx_test.go:895 UserContext 值不得跨请求泄漏
tags: [L06, typed-nil, empty-interface, reflect, DeepEqual, map-key, panic, GC]
requires: [reflect]
runtime: "<3s"
---

# 场景

把 nil `*node`、nil slice/map/func/chan、typed nil `error` 与 typed nil `io.Reader` 装箱进空接口，
逐项校验 `!= nil`、`reflect.Kind/IsNil/Type`、`errors.As/Is`、`DeepEqual`、`fmt` 输出、`map[any]`
键语义与 nil 接收者方法调用；再覆盖 nil 接口 `ValueOf` 无效、slice 接口比较 panic、未导出值等边界。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_typed_nil_empty_iface`；
2. 所有 typed nil 装箱后的接口值均 `!= nil`、`IsNil()==true`、Kind/Type 正确；
3. `errors.As` 匹配 typed nil 并得到 nil 指针，`errors.Is(typedNil, typedNil)` 为 true；
4. `DeepEqual(typedNil, typedNil)` 为 true，typed nil 与 nil 接口/不同指针类型为 false；
5. `map[any]` 中 nil 与 typed nil 是不同键，相同 typed nil 合并；slice 作接口比较必须 panic；
6. nil 接收者方法安全调用；GC 后 typed nil 与接口键 map 状态不变，五个 profile 一致。

## 失败判读

- typed nil 被判定为 `nil`：接口 nil 判定/装箱错误；
- `IsNil`/Kind 不符：reflect 对 nil 接口/typed nil 的分类错误；
- `errors.As` 或断言 panic：错误接口/类型断言对 typed nil 的处理错误；
- 接口键 map 行为异常：接口 hash/相等实现错误。

## 参数

- `UAF_SEED`：随机种子，决定检查顺序；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_typed_nil_empty_iface/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
