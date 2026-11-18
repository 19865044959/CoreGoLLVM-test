---
id: l06_error_wrap_chain
dim: L06_reflect_iface
priority: P1
sources:
  - gin@v1.10.0: errors_test.go:115 两层 fmt.Errorf("%w") 包裹自定义 error，errors.Is/As 取内层
  - echo@v4.13.4: echo_test.go:1302 HTTPError 的 SetInternal/WithInternal/Unwrap 错误链
tags: [L06, error-chain, errors.Is, errors.As, errors.Join, GC]
requires: []
runtime: "<10s"
---

# 场景

自定义 `ptrError`（持 `*errCtx`/`*node`）、`codedError`（实现 Is 按 Code 匹配），用 `fmt.Errorf("%w")` 叠加 4–8 层，再用 `errors.Is/As/Unwrap` 解包；另用 `errors.Join` 合并两条链。所有根错误放入全局切片并显式 GC 后继续解包。

# 期望行为

1. `errors.Is` 能按指针身份命中 base，也能按自定义 Is 命中同 Code 的新 target，Code 不同则 false；
2. `errors.As` 取到 `*ptrError`/`*codedError`，其 `ctx.Detail` 指针与值跨 GC 正确；
3. Unwrap 链长度为 layers+3，完整错误文本与构造顺序一致；
4. `errors.Join` 两条链后 Is/As 仍能命中各自内部错误；
5. `errors.Is(err,nil)=false`、`errors.Is(nil,nil)=true`；
6. 最后一行 `ALL PASS l06_error_wrap_chain`。

# 失败判读

- `errors.Is base pointer` / `errors.As ... identity`：`%w` 包装丢失 cause 指针或被回收；
- `payload ... after GC`：error 对象里的指针字段未被扫描；
- `unwrap chain length` / `error text`：链结构被改写；
- `errors.Join ...`：多错误展开路径错误。

# 参数

- `UAF_SEED`：与轮次组合决定包装层数（4..8，runner 注入），失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_error_wrap_chain/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
