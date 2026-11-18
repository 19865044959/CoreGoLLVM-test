---
id: l06_type_assert_loop
dim: L06_reflect_iface
priority: P0
sources:
  - fiber@v2.52.6: router_test.go:306 确认 *App/*Group 实现 Router 接口并断言
  - caddy@v2.10.0: modules_test.go:22 全局模块表查询后的接口断言
tags: [L06, type-assertion, comma-ok, nil-interface, panics, GC]
requires: []
runtime: "<3s"
---

# 场景

覆盖类型断言的各条路径：nil 接口断言、typed nil 装箱、成功/失败 comma-ok 断言、接口到接口断言、非 ok 断言失败 panic + recover，以及 80 轮带 ok 的混合类型断言循环。循环内按 UAF_SEED 打乱元素顺序并周期性 GC。

# 期望行为

1. nil 接口断言得到零值且 `ok=false`；typed nil 装箱后接口非 nil，断言回指针为 nil；
2. 指针成功断言为 `reader`/`closer`，结果分别为 10 和 -7；
3. 非 ok 形式的失败断言触发 panic 且被 recover；
4. 循环中每轮命中 9 次断言、7 次 `any` 断言，计数固定；
5. 原始 `*file` 与其 `*string` 字段跨全部 GC 有效；
6. 最后一行 `ALL PASS l06_type_assert_loop`。

# 失败判读

- `nil interface is nil` / `typed nil`：接口 nil 判定或装箱语义错误；
- `failed assertion panics`：panic 路径缺失，属于运行时行为异常；
- `reader assertion result corrupted`：断言得到的接口数据指针在 GC 后失效；
- `loop assertion matched count`：断言结果依赖顺序或类型元数据损坏。

# 参数

- `UAF_SEED`：循环内元素顺序随机种子（runner 注入），失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_type_assert_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
