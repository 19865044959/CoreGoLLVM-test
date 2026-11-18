---
id: l01_error_result_union
dim: L01_generics
priority: P1
sources:
  - pgx@v5.7.4: rows_test.go:432 列数不匹配时返回确定错误并保留错误上下文
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/marshaler_test.go:62 解码错误传播、坏输入不得 panic
  - go-redis@v9.8.0: error_test.go:25 按错误类别（EOF/池超时/LOADING/READONLY）分类重试
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 生成代码的错误返回链路
tags: [L01, generics, error-union, result-type, errors.Is, errors.As, errors.Join, multi-type-params, gc]
requires: [generics]
runtime: "<10s"
---

# 场景

用 `result[T any, E error]` 表达 ok/err 二选一 union：`okRes/errRes` 构造、`Val/Err/IsOK` 取值、`fold` 三类型参数折叠。错误类型 `qerr` 带 kind 枚举与 `detail *pcell`，实现 `Error/Is` 使 `errors.Is` 可按 union 成员匹配；`classify[E error]` 按动态类型分类。构造 120+ 条混合结果（含 `result[int64,*qerr]` 第二实例化），显式 GC 后校验 ok 值指针、错误 kind、detail 指针、包装链/Join 中的成员识别与 fold 分支。另含 fail/success 语义：err 结果不得暴露 value，ok 结果不得暴露 error。

# 期望行为

1. ok 结果的 Val 指针与写入的 `*pcell` 相同，GC 后字段与 blob 正确；err 结果的 Val 返回 ok=false；
2. err 结果的 Err 非 nil，动态类型仍为 `*qerr`，kind 与构造时一致，detail 指针身份跨 GC 不变；
3. `errors.Is` 命中对应 kind 的 sentinel、不误命中其他成员；`errors.As` 取回的 `*qerr` 与原指针相同；
4. 两层 `fmt.Errorf("%w")` 包装与 `errors.Join` 后仍能识别 union 成员，detail 指针不变；
5. `result[int64,*qerr]` 的值与错误分支均正确；`fold` 在 ok/err 分支分别返回预期值；
6. 全部检查通过，退出码 0。

# 失败判读

- `Val exposed` / `Err exposed`：union 判别位被破坏，检查 ok 字段与 E 的零值语义；
- `errors.Is failed`：`Is` 方法的 kind 比较或包装链断链；
- `detail pointer identity changed`：错误对象内的指针未保活，重点检查结果切片与错误链的可达性。

# 参数

- `UAF_SEED`：决定结果数量（120+seed%16），失败输出带种子；
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_error_result_union/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
