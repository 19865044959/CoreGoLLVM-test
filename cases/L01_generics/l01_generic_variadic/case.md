---
id: l01_generic_variadic
dim: L01_generics
priority: P0
sources:
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/pipe_test.go:30 NewPipe[int] 批量写入 5 个元素、读端按序累积
  - kitex@v0.13.0: pkg/remote/trans/ttstream/container/stack_test.go:88 泛型容器批量插入/删除后的 Size 与遍历一致性
  - pgx@v5.7.4: rows_test.go:335 CollectRows 一次性收集全部行到泛型结果切片
tags: [L01, generics, variadic, slice-expansion, pointer-elements, gc]
requires: [generics]
runtime: "<3s"
---

# 场景

泛型可变参数函数的多种调用形态：`Collect[T](ps ...*T)` 的 slice 展开与逐个实参、`Collect[T]()` 空参数、混入 nil 元素、`FirstOr` 默认值；泛型类型方法 `Bag[T].AddMany(ps ...*T)` 分段展开并在 GC 后追加；以及变参元素本身为切片的 `AppendPairs[T](dst, groups ...[]*T)`。对应 kitex 泛型管道批量写与 pgx CollectRows 的收集语义。

# 期望行为

1. `Collect(orig...)` 返回长度/顺序/指针身份与输入一致，GC 后 payload 完好；
2. 空 variadic 返回空切片；nil 元素保留；`FirstOr` 在无参/首参 nil 时返回默认指针；
3. `Bag.AddMany` 两次分段展开后元素顺序等于拼接结果，GC 后追加 2 个元素位置正确；
4. `AppendPairs` 多组展开后顺序正确、指针身份不变；
5. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_variadic`。

# 失败判读

- `Collect element pointer identity changed`：变参打包切片时指针被复制错/回收；
- `explicit variadic order corrupted`：编译器对可变参数与 slice 展开的求值顺序错误；
- `Bag element ...`：方法可变参数追加后的切片底层数组被过早回收；
- `nil variadic element not preserved`：变参空值处理错误。

# 参数

- `UAF_SEED`：决定元素数（32..40），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_variadic/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
