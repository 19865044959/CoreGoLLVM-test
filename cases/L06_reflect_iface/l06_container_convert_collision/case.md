---
id: l06_container_convert_collision
dim: L06_reflect_iface
priority: P2
sources:
  - gin@v1.10.0: binding/form_mapping_test.go:222 MakeSlice + 逐值 ParseInt 构造强类型切片
  - gorm@v1.30.0: clause/expression_test.go:16 struct/map 参数展开为 SQL 变量
  - pgx@v5.7.4: pgtype/hstore_test.go:266 hstore map 文本/二进制往返
  - echo@v4.13.4: bind_test.go:442 绑定结果写入 map[string]string
tags: [L06, container-conversion, collision, map-key, atomicity, NaN, nil-container, pointer-elements, GC, torture]
requires: []
runtime: "<60s"
---

# 场景

把 JSON/query 解码出的 `[]any`/`map[string]any`/异构 map 转成强类型容器时，key 解析可能把
不同输入映射到同一输出 key（"1" 与 "01"、不同动态类型的接口 key、多个 NaN 格式化为 "NaN"）。
本用例实现字符串 key、接口 key、float key、嵌套容器四条转换管线，要求冲突确定报错且不返回
部分结果，并验证 nil vs 空容器、指针元素跨 GC。

# 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_container_convert_collision`；
2. `"01"` 与 `"1"` 解析冲突时返回确定错误 `duplicate converted key "1" -> 1`，结果为 nil，
   调用方原目标 map 保持不变；
3. 接口 key 中 `string("k")` 与 `myString("k")` 转换后同名时冲突；不支持的 key 类型报错；
4. 两个 NaN key 在源 map 中是两个条目，格式化为同一 key 后冲突；有限 float key 正常转换；
5. 切片转换遇到非法元素时整体失败（元素下标确定），nil 切片保持 nil、空切片得到空非 nil；
6. 嵌套 `map[string]any` 的字段/元素错误带字段名包装且顺序确定；
7. 成功转换出的 `*node` 只由结果 map 保活，显式 GC 后值与标签仍正确；
8. 默认 10 轮 ×32 项由 `TORTURE_ROUNDS`/`TORTURE_ITEMS` 放大，每轮冲突检测与成功转换均稳定。

# 失败判读

- `key collision ...` / `interface key collision` / `NaN keys collide`：冲突检测缺失或迭代顺序
  导致错误不确定；
- `failed conversion leaves target unchanged`：转换写入共享目标，破坏原子性；
- `nil source ...` / `empty source ...`：nil 与空容器语义混淆；
- `... after GC`：结果 map 中的指针值未被扫描；
- 错误文本不符：确定性排序或错误包装缺失。

# 参数

- `UAF_SEED`：成功路径节点标签的种子后缀；失败行带出种子；
- `TORTURE_ROUNDS`：轮数（默认 10，上限 2000）；
- `TORTURE_ITEMS`：每轮生成的 key 对数（默认 32，上限 20000）。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_container_convert_collision/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=500 TORTURE_ITEMS=4096 UAF_SEED=7 /tmp/case
```
