---
id: l05_iface_boxing_pressure
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: ast/api_native_test.go:132 的 TestStackAny：//go:noinline 把值装箱成 any，GC 后再断言/序列化
tags: [L05, interface-boxing, any, type-switch, allocation, gc]
requires: []
runtime: "<10s"
---

# 场景

通过 `//go:noinline box(any) any` 把 int64、string、`*node`、`[]byte`、值类型 struct 五类值装箱进 20000 个 `[]any`，每 4000 个分配垃圾并 GC；GC 后逐个 type switch 还原并校验内容/指针身份。另构造 800 项 `map[string]any` 混合装箱，并验证丢弃大部分接口数组后保留项仍有效。

# 期望行为

1. 20000 个装箱值在 GC 后类型与内容全部正确（含切片逐字节、结构体指针字段）；
2. 同一 `*node` 经普通赋值与 noinline 函数装箱后，接口内指针身份相同且字段正确；
3. `map[string]any` 中 800 个装箱值跨 GC 后类型/内容不变；
4. int64 装箱计数与期望一致；
5. 丢弃 `items` 后仅保留前 5 项，GC 后保留项仍正确；
6. 最后一行输出 `ALL PASS l05_iface_boxing_pressure`。

# 失败判读

- `boxed item=N corrupted`：接口数据字或指针位图错误；
- `interface identity lost`：装箱路径复制了指针或缓存了错误 itab；
- `map boxed key=N corrupted`：map 中的接口值被破坏；
- `saved boxed item`：仍被引用的装箱对象被提前回收（UAF）。

# 参数

- `UAF_SEED`：控制 int64 偏移与 []byte 内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 20000 项 + 800 项 map。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_iface_boxing_pressure/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
