---
id: l12_init_errors_join
dim: L12_init_link
priority: P1
sources:
  - tailscale@v1.80.2: cmd/k8s-operator/e2e/main_test.go:75 err = errors.Join(err, cleanup())
  - golangci-lint@v2.0.2: test/configuration_file_test.go:53 errAll = errors.Join(errAll, fmt.Errorf("%s: %w", abs, err))
  - nats-server: server/jetstream_helpers_test.go:2180 return errors.Join(errs...)
tags: [L12, init-error, errors.Join, multi-error, recover, error-tree, pointer-payload, gc]
requires: []
runtime: "<10s"
---

# 场景

三个 init 向固定 slot 写入初始化结果：alpha 写入包装配置错误并携带 `*detail` 指针；beta 在嵌套函数 panic 后 recover 并转换为带哨兵错误的 `taggedError`；gamma 校验正确 fixture 后保持 nil。main 按固定 slot 顺序调用 `errors.Join`，因此错误树结构与文本不依赖未定义的跨文件 init 顺序。用例遍历 multi-error、验证 `errors.Is/As`、空 Join、纯校验路径，并在 GC 后复核 error 树中的指针 payload。

## 期望行为

1. init 标记排序后恰为 `alpha,beta,gamma,main`，各一次；
2. 聚合错误非 nil，文本严格为 `alpha: bad entry 2: invalid value: -5\nbeta: recovered panic: panic sentinel`；
3. `errors.Is` 同时命中 `errBadValue` 与 `errPanicSentinel`，不误命中无关哨兵；
4. `errors.As` 取到第一个 `*taggedError`（alpha），其 `Detail` 与 init 期构造的指针同一、payload 为 `alpha-payload`；
5. 聚合错误实现 `Unwrap() []error`，子错误恰 2 个，第二个为 beta panic 错误；
6. 全 nil 的 Join 返回 nil；单 slot/双 slot 聚合文本稳定；
7. 纯校验路径：合法 fixture 返回 nil；负值/重复名/空名分别 `errors.Is` 命中且文本确定；
8. 8 goroutine 并发读错误树 + 显式 GC 期间文本、哨兵与 Detail 指针身份不变；
9. 最后一行 `ALL PASS l12_init_errors_join`。

## 失败判读

- 最终文本随 init 顺序变化：聚合依赖了未定义顺序而非固定 slot；
- `errors.Is/As` 失败：`%w` 包装或 multi-error 展开错误；
- 子错误数量不符：Join 跳过了 nil 或重复包装；
- GC 后 Detail 指针或 payload 变化：错误树中的全局指针未被正确扫描；
- 并发阶段失败：错误树在 init 后被修改（应保持不可变）。

## 参数

- `UAF_SEED`：决定并发验证的轮换起点与 GC 轮数；断言与种子无关。

## 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_init_errors_join/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
