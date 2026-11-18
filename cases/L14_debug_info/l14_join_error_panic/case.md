---
id: l14_join_error_panic
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_join_error_panic/main.go:30 自定义 Unwrap() []error 多分支类型
  - cases/L14_debug_info/l14_join_error_panic/main.go:43 自定义 Unwrap() error 且携带 payload 的类型
  - cases/L14_debug_info/l14_join_error_panic/main.go:54 errors.Join 复合错误后 panic
  - cases/L14_debug_info/l14_join_error_panic/main.go:69 recover 后 errors.Is/errors.As 与 traceback 断言
  - docs/DIMENSIONS.md:22 L14 调试信息（wrapped error panic）
tags: [L14, wrapped-error, errors.Join, multi-unwrap, errors.Is, errors.As, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

panic 携带由 `errors.Join` 合并、并同时包含自定义 `Unwrap() []error` 多分支包装与 `Unwrap() error` 单链包装的复合错误。模拟服务在异常路径上把多个下游错误聚合后崩溃的现场。recover 后 `errors.Is` 必须同时命中两条分支的 sentinel，`errors.As` 必须取出带 payload 的自定义类型；`debug.Stack` 必须给出包装链每一层的真实符号与 `main.go:行号`。

## 期望行为

1. 被 recover 的值实现 error，且错误文本同时包含 `alpha-wrap`、`beta-wrap`、`typed-wrap`；
2. `errors.Is(err, errAlpha)` 与 `errors.Is(err, errBeta)` 均为 true（跨 Join 与多分支 Unwrap）；
3. `errors.As` 取出 `*joinTyped`，code==77，node 指向原 payload 且 next 链有效；
4. 静态构造的 Join + `%w` 链同样可命中两个 sentinel，外层再 `%w` 包装后仍可命中；
5. 同文本但不同实例的错误不会误命中 sentinel；
6. traceback 包含 `main.joinLeaf(`、`main.joinMid(`、`main.joinTop(`、`goroutine` 头，且至少 3 处 `main.go:<行号>`；
7. 两次 GC 后 payload 与 next 链仍有效；退出码 0，最后一行 `ALL PASS l14_join_error_panic`。

## 失败判读

- 某 sentinel 未命中：`Unwrap() []error`/Join 遍历实现错误；
- `errors.As` 失败或 payload 错误：类型包装链未正确展开；
- 错误文本缺少分支：Join 拼接或 Error 实现错误；
- traceback 符号缺失：包装函数帧调试信息缺失；
- payload 失效：panic 错误对象中的指针未被标记。

## 参数

无随机路径；`UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_join_error_panic/*.go
GODEBUG=clobberfree=1 /tmp/case
```
