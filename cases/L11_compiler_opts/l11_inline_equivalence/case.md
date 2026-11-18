---
id: l11_inline_equivalence
dim: L11_compiler_opts
priority: P0
sources:
  - klauspost-compress@v1.17.11: gzip/gzip_test.go:581 每轮新建 Writer 压缩小载荷，验证逃逸/内联敏感性
  - grpc-go@v1.71.1: benchmark/primitives/primitives_test.go:449 接口方法调用 vs 具体类型方法调用（去虚化/内联）
tags: [inline, noinline, go:noinline, pointer-after-gc, alias-in-slice, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟压缩/序列化热路径里“编译器是否内联”不会改变语义的契约：同一算术函数分别提供普通版本与 `//go:noinline` 版本，对 14×14 组有符号输入（含 `-2^62`、`2^62-1` 等溢出边界）逐项比较；两个构造函数分别返回 `*pair`，两个取指针函数分别返回切片元素地址与局部数组元素地址。所有指针在显式垃圾分配 + 两次 `runtime.GC()` 后逐个校验字段、身份与独立性。

# 期望行为

1. `mix` 与 `mixNoInline` 对全部输入结果完全相等；
2. 两种 `sliceSum` 对正序/逆序切片结果一致且和不变；
3. 构造函数返回的指针非 nil、字段正确，不同调用得到不同对象；
4. `pick`/`pickNoInline` 返回的 `&slice[i]` 在 GC 后仍等于 `&pairVals[i]`，目标字段不变；
5. 返回局部数组元素指针的两条路径在 GC 后分别保持 `a/b/tag` 期望值，且对象互异；
6. 最后一行输出 `ALL PASS l11_inline_equivalence`。

# 失败判读

- `mix/mixNoInline result mismatch`：内联与未内联代码生成语义不一致（算术溢出/位移/分支折叠错误）；
- `pointer into slice changed identity` / `target corrupted`：切片元素指针在 GC 后失效或地址被错误复制；
- `local array element corrupted`：返回的局部数组元素对象未被正确堆分配或栈对象被提前回收；
- 崩溃或 `fatal error`：栈映射/写屏障问题。

# 参数

- 无随机路径，不读取 `UAF_SEED`；
- 不依赖内联一定发生：两种构建（默认优化、`-N -l`）下结果都必须一致。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_inline_equivalence/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_inline_equivalence/*.go && /tmp/case0
```
