---
id: l06_reflect_container_convert
dim: L06_reflect_iface
priority: P2
sources:
  - gorm@v1.30.0: callbacks/create_test.go:16 反射区分 slice/array/struct 并逐元素构造行值
  - gorm@v1.30.0: clause/expression_test.go:16 struct/map 参数展开为 SQL 变量
  - pgx@v5.7.4: pgtype/hstore_test.go:266 hstore map 文本/二进制往返
  - gin@v1.10.0: binding/form_mapping_test.go:222 reflect.MakeSlice + 逐值 ParseInt 构造切片
tags: [L06, reflect, Convert, container-conversion, map-rebuild, key-conversion, nil-container, error-path, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

JSON/query 解码得到的 `[]any`/`map[string]any` 需要逐元素转换成强类型容器；框架还会把
同名底层的命名 slice/map 直接 `Convert`，而 key 类型变化时必须重建 map。本用例实现
`convertSlice`/`convertMap` 两条反射转换管线，覆盖可整体 Convert、可逐元素 Convert、
不可 Convert、nil 容器、nil 元素错误路径，并让指针元素跨 GC 保活。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_reflect_container_convert`；
2. `[]int` 不能整体转 `[]int64`，但逐元素可转；`[]int→namedInts`、`map[string]int→intMap`
   因底层类型一致可整体 Convert；
3. `[]any{int,int64}` → `[]int64`、`[]string` → `[]myString` 逐元素转换值和元素类型正确；
4. `map[string]any` → `map[string]int64`、`map[int]string` → `map[myInt]string`（重建 key）
   结果正确；nil 容器整体 Convert 保留 nil，逐元素转换得到空而非 nil；
5. 非法元素（string→int64、nil 接口、nil map 值）返回错误而非 panic；
6. 指针 map 转换后元素在 GC 后仍可解引用并读到原值；
7. 默认 20 轮 ×64 元素在 60s 内完成，`TORTURE_ROUNDS`/`TORTURE_ITEMS` 可放大。

## 失败判读

- `requires element conversion` / `not whole-convertible`：整体可转换性判定错误；
- `elementwise ...`：逐元素 `Convert` 或 `Append` 结果错误；
- `map key conversion rebuild`：SetMapIndex 重建路径错误；
- `nil ... preserves nil` / `yields empty`：nil 容器语义错误；
- `... fails` 缺失：非法元素未返回错误；
- `pointer map value after GC`：map 中的指针值未被扫描。

## 参数

- `UAF_SEED`：源容器元素值与指针负载来源；失败输出带出种子；
- `TORTURE_ROUNDS`：轮数（默认 20，上限 2000）；
- `TORTURE_ITEMS`：每轮元素数（默认 64，上限 20000，最小 8）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_reflect_container_convert/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=500 TORTURE_ITEMS=4096 UAF_SEED=7 /tmp/case
```
