---
id: l17_method_named_reflect_sink
dim: L17_stackmap
priority: P1
sources:
  - gorm@v1.30.0: callbacks/create_test.go:53（ReflectValue: reflect.ValueOf(dest)，反射写回目标对象）
  - gorm@v1.30.0: schema/schema_test.go:38（TestParseSchemaWithPointerFields，指针字段模型解析）
tags: [L17, stackmap, method, named-return, defer-sink, reflect, interface-dispatch, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

方法 `recorder.run` 有命名返回 `out record`，其中含指针、指针切片与 any 字段。注册三个 defer：接口方法 `fill(&out)` 经 itab 写回命名返回；另一个 defer 用 `reflect.ValueOf(&out).Elem()` 逐字段写入 V/P/List/Any/Tag；第三个 defer 在注册时按值快照 `out`。defer 内均执行两轮 GC；注册后把接口变量重绑定到另一个实现。

`UAF_SEED` 决定每轮 base 与初始 any 形态。

# 期望行为

1. 三个 defer 按 LIFO 执行，顺序为 `snap`、`reflect`、`fill`；
2. 快照 defer 看到的仍是初始 seed record，且 GC 后其中指针/切片有效；
3. 接口 fill 使用注册时捕获的旧 receiver（tag 为 `fill`），而非重绑定后的实现；
4. 命名返回最终字段为 reflect 与 interface sink 叠加后的结果：V=base+20、P=fill keep、List 长度 3 且末元素为 fill keep、Any 为 fill boxed；
5. caller 在返回后再次 GC，命名返回的指针/切片/interface 字段完整；
6. 退出码 0，最后一行 `ALL PASS l17_method_named_reflect_sink`。

# 失败判读

- `snapshot sum`：快照 defer 的 struct 副本或命名返回地址未被扫描；
- `defer order`：defer LIFO 顺序异常；
- `named return P/List/Any`：defer 中通过接口/reflect 写回的指针丢失；
- `named return scalar`：reflect 对命名返回槽的写入未保留；
- SIGSEGV / 坏指针：`&out` 逃逸到 defer 记录后栈图未标记。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 6 轮，初始 any 在 pair 与 string 间轮换。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_method_named_reflect_sink/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
