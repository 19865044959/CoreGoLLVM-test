---
id: l06_error_custom_as
dim: L06_reflect_iface
priority: P1
sources:
  - gin@v1.10.0: errors_test.go:115 两层 fmt.Errorf("%w") 包裹自定义 Error 后 errors.Is/As
  - webrtc@v4.1.2: internal/util/util_test.go:18 多错误扁平化、去 nil、Is 命中子错误
  - echo@v4.13.4: echo_test.go:1302 HTTPError 的 SetInternal/WithInternal/Unwrap 链
  - echo@v4.13.4: json_test.go:53 errors.As 从 JSON 错误提取 SyntaxError/UnmarshalTypeError
tags: [L06, reflect, error, errors.As, custom-As, multi-wrap, Join, forest, typed-nil, panic, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

网关错误处理器需要按错误码/类型路由：既依赖标准 `errors.As` 解包，也会让错误类型实现
`As(any) bool` 把内部错误映射为对外错误视图。本用例让 `routeErr` 通过自定义 As 合成链上
不存在的 `*routeTarget`，覆盖 Join 森林左到右顺序、多 `%w` 分支、`Unwrap() []error`、
nil/typed nil 分支与 As 目标校验 panic，并在 GC 后复核标签负载。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_error_custom_as`；
2. `routeErr` 实现 error 与 `As(any) bool`，`boxErr` 无自定义 As，`forestErr` 提供
   `Unwrap() []error`；
3. 自定义 As 能合成链上不存在的 `*routeTarget`（携带 tag 指针），但不影响 `errors.Is`；
   标准 As 仍可直接取到具体 `*routeErr`；
4. Join 与多 `%w` 均按左到右/深度优先顺序遍历；多 `%w` 的 `errors.Unwrap` 返回 nil，
   `Unwrap() []error` 返回两个分支，`Error()` 文本确定；
5. `errors.Join(nil,nil)` 为 nil，Join 过滤 nil 但保留 typed nil 分支；typed nil 的
   As/Is 语义正确，自定义 As 对 nil 接收者安全返回 false；
6. `errors.As` 对 nil target、非指针 target、指向非 error 的指针均 panic；
7. 每轮的 Join/多 %w 错误树、自定义 As 目标与标签指针在 GC 后仍可读取。

## 失败判读

- `custom As synthesizes target` / `custom As payload`：自定义 As 未被调用或未设置目标；
- `... target not visible to Is`：As 被错误用于 Is；
- `Join ... left to right` / `multi-wrap branch order`：森林遍历顺序错误；
- `multi-wrap Unwrap returns nil`：多 %w 被错误当成单链；
- `Join keeps typed nil branch` / `typed nil ...`：typed nil 被过滤或误判；
- `... did not panic`：As 目标校验缺失。

## 参数

- `UAF_SEED`：每轮 route/tag 值来源；失败输出带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_error_custom_as/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
