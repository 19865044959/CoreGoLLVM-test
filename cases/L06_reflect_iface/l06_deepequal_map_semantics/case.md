---
id: l06_deepequal_map_semantics
dim: L06_reflect_iface
priority: P2
sources:
  - caddy@v2.10.0: admin_test.go:51 JSON any 树的 map 增删改与整树比较
  - gorm@v1.30.0: statement_test.go:11 WHERE 条件 clone 后 map/slice 互不污染
  - pgx@v5.7.4: pgtype/hstore_test.go:266 map↔文本/二进制往返后的等值比较
tags: [L06, reflect, DeepEqual, map, NaN, func-value, interface-key, cycle, GC, torture]
requires: [reflect]
runtime: "<60s"
---

# 场景

固定语义覆盖 `reflect.DeepEqual` 的 map 规则：nil vs 空 map、插入顺序无关、NaN 键/值、非 nil func
永不相等、`int` 与 `int64` 接口键区分、typed nil 与 nil 值、自引用 map 环、struct 键与指针值。
随后多轮构造两份插入顺序相反的等值 map（指针/切片/嵌套 map/ nil 值混合），做增删改、指针目标
修改与 GC 后重复比较；默认 12 轮 ×40 键，可放大。

## 期望行为

1. 退出码 0，最后一行输出 `ALL PASS l06_deepequal_map_semantics`；
2. 不同插入顺序、不同指针身份的等值 map 比较为 true；自引用环不挂起；
3. nil 与空 map 不等、双 nil 相等；NaN 键/值的跨 map 比较恒为 false，同一 map 对象为 true；
4. 非 nil func 值永不相等，双方 nil 才相等；接口键按动态类型匹配；
5. 修改/删除任一键立即 false，恢复后重新 true；
6. GC 后 map 内容、指针叶子和嵌套 map 完整，重复比较结果不变；
7. 默认 12×40 在 60s 内完成，支持 `TORTURE_ROUNDS`/`TORTURE_WIDTH` 放大。

## 失败判读

- `equal maps in different insertion order`：比较依赖遍历顺序，或 map 指针元素比较错误；
- `NaN ... never equal`：NaN 语义被错误特判；
- `cyclic maps deeply equal` 挂起/栈溢出：visited 集合缺失，无法处理环；
- `pointer map value after GC`：map value 中的指针未被扫描或被覆盖。

## 参数

- `UAF_SEED`：插入顺序与值来源；失败行带出种子；
- `TORTURE_ROUNDS`：轮数（默认 12）；
- `TORTURE_WIDTH`：每份 map 键数（默认 40，最小 10）。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_deepequal_map_semantics/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=200 UAF_SEED=7 /tmp/case
```
