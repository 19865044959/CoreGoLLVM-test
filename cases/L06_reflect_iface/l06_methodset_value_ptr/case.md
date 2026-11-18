---
id: l06_methodset_value_ptr
dim: L06_reflect_iface
priority: P0
sources:
  - gorm@v1.30.0: schema/callbacks_test.go:22 反射方法集探测生命周期回调（值/指针接收者规则）
  - echo@v4.13.4: context_test.go:2686 嵌入 ResponseWriter 接口的包装结构
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 接口包装与动态分派
tags: [L06, method-set, value-receiver, pointer-receiver, method-value, embedding, GC]
requires: [reflect]
runtime: "<3s"
---

# 场景

`counter` 同时含指针接收者方法（`Inc/PtrOnly`）与值接收者方法（`Get/ValOnly`），分别定义
接口并在运行时用 `reflect.Type.NumMethod/MethodByName/Implements` 检查方法集；再以太、值两种
方式匿名嵌入 `counter`，验证提升规则。方法表达式、绑定方法值与接口方法调用均跨越显式 GC。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_methodset_value_ptr`；
2. `counter` 值方法集只含 2 个值方法，`*counter` 含全部 4 个；接口实现关系与编译期 `var _` 一致；
3. `embed *counter` 值提升全部方法，`embed counter` 值只提升值方法，`*embed counter` 全部提升；
4. 方法表达式首参为接收者，绑定到值接收者的方法保留副本，绑定到指针接收者的方法跟随接收者；
5. GC 后绑定方法值/接口值仍可调用，接收者中的 `*node` 字段完整；
6. 运行时间 <3s，五个 profile 结果一致。

## 失败判读

- `NumMethod` 数量错误：方法集计算（值/指针接收者、嵌入提升）错误；
- `Implements` 与编译期结论不一致：itab 生成或接口满足判定错误；
- 绑定方法调用 panic/结果错：方法值闭包捕获了错误接收者（副本 vs 指针）；
- GC 后崩溃：方法值/接口数据区未保活或写屏障缺失。

## 参数

- `UAF_SEED`：随机种子，控制增量与字段值；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_methodset_value_ptr/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
