---
id: l12_errors_join_nested
dim: L12_init_link
priority: P1
sources:
  - golangci-lint@v2.0.2: test/configuration_file_test.go:53 errAll = errors.Join(errAll, fmt.Errorf("%s: %w", abs, err))
  - nats-server@v2.11.0: server/jetstream_helpers_test.go:2180 return errors.Join(errs...)
  - fiber@v2.52.6: middleware/timeout/timeout_test.go:24 fmt.Errorf("%w: l2 wrap", fmt.Errorf("%w: l1 wrap ", err)) 嵌套错误链
  - protobuf-go@v1.36.6: types/known/durationpb/duration_test.go:100 textError 自定义 Is 匹配
tags: [L12, init-error, errors.Join, nested-join, multi-w, custom-Is, errors.As, error-tree, pointer-payload, gc]
requires: []
runtime: "<10s"
---

# 场景

四个 init 向固定 slot 写入：alpha 用 `fmt.Errorf` 双 `%w` 包装并携带 `*detail`；beta 为嵌套 `%w` 链；gamma 为 `errors.Join`（含自定义 `Is` 的 `codeError`）；delta 为嵌套 Join。slot 为空时 Join 忽略。main 按固定 slot 顺序聚合，因此文本与 init 的未定义顺序无关；随后验证树形、`Is/As`、重复 Join、nil 消除、指针 payload 与显式 GC。

## 期望行为

1. init slot 集合恰为 `{alpha,beta,delta,epsilon,gamma}`，`slotE==nil`；
2. 聚合文本严格为 7 行：`load: alpha: entry 2: bad value` / `beta: panic: panic sentinel` / `gamma: gamma` / `code 7` / `bad value` / `gamma` / `delta: alpha`；
3. 顶层 `Unwrap() []error` 恰 4 个子错误且顺序/身份为 slotA..slotD；alpha 的双 `%w` 子节点有 2 个、gamma 有 2 个、delta 有 2 个且首子为嵌套 Join；
4. `errors.Is` 同时命中 errAlpha/errBadValue/errPanicSentinel/errGamma 与自定义 `errCode`，不误命中无关哨兵；
5. `errors.As` 取到 `*taggedError`（Detail 指针与 init 期同一、payload `payload-a`）与 `*codeError`（code 7、`code-payload`）；
6. `errors.Join()`/全 nil Join 返回 nil；单元素 Join 文本等于原错误；重复 Join 保留两个相同子错误且 `Is` 成立；
7. 8 goroutine 并发读 Error/Is/As + 显式 GC 后文本、哨兵匹配与 Detail 指针身份不变；
8. 最后一行 `ALL PASS l12_errors_join_nested`。

## 失败判读

- 文本随 init 顺序变化：聚合依赖未定义顺序而非固定 slot；
- `Is/As` 失败：multi-error 展开、双 `%w` 或自定义 `Is` 语义错误；
- 顶层/嵌套子错误数量不符：Join 跳过 nil、扁平化嵌套或重复包装错误；
- GC 后 Detail 指针或 payload 变化：错误树中的全局指针未被扫描；
- 并发阶段失败：错误树在 init 后被修改（应保持不可变）。

## 参数

- `UAF_SEED`：决定并发验证的轮换起点与 GC 轮数；断言与种子无关，失败行带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_errors_join_nested/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
