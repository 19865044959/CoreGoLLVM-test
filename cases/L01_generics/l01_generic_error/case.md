---
id: l01_generic_error
dim: L01_generics
priority: P1
sources:
  - ent@v0.14.4: dialect/gremlin/encoding/graphson/marshaler_test.go:62 Unmarshaler 接口解码错误传播、坏输入不得 panic
  - pgx@v5.7.4: rows_test.go:432 RowToStructByPos[person] 列数不匹配时返回确定错误
  - sqlc@v1.28.0: internal/endtoend/endtoend_test.go:107 生成代码中的错误返回与 wrap 链路
tags: [L01, generics, generic-error, error-chain, errors.Is, errors.As, errors.Join, gc]
requires: [generics]
runtime: "<10s"
---

# 场景

泛型错误类型 `gerr[T comparable]` 实现 `Error/Unwrap/Is`，泛型构造器 `Wrap[T]` 与解析器 `Find[T]` 基于 stdlib `errors`。每轮构造 `gerr[int64]`（code=404，挂 payload）→ 两层 `fmt.Errorf("%w")` → `gerr[string]`（code=E_API）→ `errors.Join` 的链，GC 后验证 Is/As/Join、错误文本、unwarp 深度与 payload 指针身份；另测未包装错误的 nil cause。

# 期望行为

1. `errors.Is` 能命中 sentinel、两种实例化的自定义 code，code 不符时为 false；
2. `Find[int64]`/`Find[string]` 经 `errors.As` 取到对应实例的 payload，指针身份与内容跨 GC 正确；`Find[int32]` 不匹配；
3. 错误文本 `api: outer: sql: db: base failure`，unwrap 深度为 5，首层 Unwrap 身份不变；
4. `errors.Join` 不破坏内部 error 链的 Is/As；
5. 无 cause 的泛型错误 Unwrap 返回 nil、Is 按 code 匹配；
6. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_error`。

# 失败判读

- `custom Is[...] did not match`：泛型类型断言 `target.(*gerr[T])` 在实例化后被错误折叠；
- `Find[...] payload pointer identity changed`：error 接口里的泛型结构体指针字段未被扫描；
- `error text corrupted`：`%w` 链在 GC 后丢失 cause；
- `unwrap depth`：错误链被截断；
- `unexpectedly matched`：不同 T 的实例被错误视为同一类型。

# 参数

- `UAF_SEED`：决定错误链构造轮数（40..50），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_error/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
