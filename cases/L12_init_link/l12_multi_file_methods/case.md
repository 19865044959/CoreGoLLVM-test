---
id: l12_multi_file_methods
dim: L12_init_link
priority: P1
sources:
  - gorm@v1.30.0: schema/callbacks_test.go:22 反射/方法集探测生命周期回调
  - fiber@v2.52.6: router_test.go:306 接口实现编译期断言
tags: [L12, multi-file, methods, interface, embedding, method-set, dispatch, gc]
requires: []
runtime: "<10s"
---

# 场景

`circle` 的类型定义、Area/Perimeter/Name 方法分别放在不同文件；`rect` 的全部方法为指针接收者且分散在两个文件。通过编译期 `var _ Shape` 断言与运行期 type switch 校验方法集规则、嵌入方法提升、Name 覆盖，以及接口派发在 GC 后的正确性。

# 期望行为

1. 编译期方法集断言成立（circle 值/指针、\*rect、namedCircle 均实现 Shape）；
2. 初始派发：面积 21、周长 28、名字串 `circle|rect|unit`；
3. rect 值不实现 Shape，\*rect 实现；circle 值与指针都实现；
4. namedCircle 从嵌入 circle 获得 Area/Perimeter，Name 被覆盖，Tag 被提升；
5. type switch 精确区分 circle/\*rect/namedCircle 各 1 个；
6. 修改接口持有的 \*rect 后，GC 前后派发结果反映新值（面积 27、周长 32），接口中指针身份不变；
7. 最后一行 `ALL PASS l12_multi_file_methods`。

# 失败判读

- 编译失败（`does not implement`）：跨文件方法集/嵌入方法提升实现错误；
- 运行期派发结果错误：itable/itab 构造或接口派发错误；
- rect 值断言意外成功：指针接收者方法集规则错误；
- GC 后接口值失效/内容变化：接口数据字或指针扫描错误。

# 参数

无随机路径；`UAF_SEED` 不参与本用例。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_multi_file_methods/*.go
GODEBUG=clobberfree=1 /tmp/case
```
