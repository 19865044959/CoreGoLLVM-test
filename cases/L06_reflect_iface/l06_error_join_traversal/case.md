---
id: l06_error_join_traversal
dim: L06_reflect_iface
priority: P1
sources:
  - gin@v1.10.0: errors_test.go:115 两层 fmt.Errorf("%w") 包裹后的 errors.Is/As
  - webrtc@v4.1.2: internal/util/util_test.go:18 FlattenErrs 多错误扁平化与逐子错误 Is
  - echo@v4.13.4: echo_test.go:1264 HTTPError 的 Unwrap internal 链
  - gin@v1.10.0: errors_test.go:125 errors.Is/errors.As 对包装错误的断言
tags: [L06, error, errors.Is, errors.As, errors.Join, unwrap-forest, traversal-order, custom-As, interface-target, GC]
requires: []
runtime: "<10s"
---

# 场景

网关错误路由会对 `errors.Join`/多 `%w` 形成的错误森林做 `errors.Is/As`：遍历必须深度优先、
左到右，节点自定义 `As` 返回 true 时短路、返回 false 时继续沿 `Unwrap` 下探；target 可以是
接口（`Timeout() bool`、`Unwrap() []error`）。本用例构造嵌套 Join、单链 Unwrap、自定义
Is+As、`Unwrap() []error` forest、nil 分支与多 `%w`，并记录每次 Is/As 回调顺序，跨 GC 校验
payload。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_error_join_traversal`；
2. 嵌套 `errors.Join(errors.Join(a,b),c)` 的 Error 文本为 `a\nb\nc`，`errors.Unwrap` 返回
   nil，`Unwrap() []error` 外层/内层长度均为 2；
3. 所有 probe 返回 false 时，`errors.As` 回调顺序为 `as:a,as:b,as:c`，`errors.Is` 为
   `is:a,is:b,is:c`（深度优先、左到右）；
4. `errors.As` 到 `interface{ Unwrap() []error }` 在 Join 节点直接命中，到
   `interface{ Unwrap() error }` 不命中；
5. 自定义 `As` 返回 false 后继续下探到 child 并命中；同一 error 同时实现 Is/As 时，As 返回
   true 短路 child，Is 先调用自定义 Is 再沿 Unwrap 下探；
6. forest 中的 nil 分支被安全跳过，leaf 仍可 Is/As；接口 `Timeout() bool` target 可命中；
   多 `%w` 的 `Unwrap` 返回 nil 而 `Unwrap() []error` 返回两个分支，遍历顺序为书写顺序；
7. 错误树与 payload 只由接口槽保活时，GC 后 As 结果、Tag 指针与 `Timeout` 接口调用均不变。

# 失败判读

- `... traversal is depth-first left-to-right`：森林遍历顺序错误（广度优先、右到左或跳过节点）；
- `As interface target ...` / `single-Unwrap ...`：接口 target 的 assignability 判定错误；
- `custom As false falls back` / `custom As true short-circuits`：As 返回值语义或 Unwrap 顺序错误；
- `forest ... nil branches`：nil 子错误未被安全跳过；
- `multi %w ...`：多 `%w` 的 Unwrap 形状错误；
- `... after GC`：错误链中的 Tag 指针未被保活。

# 参数

- `UAF_SEED`：各 payload 节点值来源；失败行带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_error_join_traversal/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
