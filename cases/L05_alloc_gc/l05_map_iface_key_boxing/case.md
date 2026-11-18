---
id: l05_map_iface_key_boxing
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: issue_test/issue115_test.go:44 的 TestIssue115_MarshalMapWithSort：多种数值 map key 类型与极端值
  - sonic@v1.13.2: ast/api_native_test.go:132 的 TestStackAny：值装箱成 any 后 GC/断言
tags: [L05, map-churn, interface-key, boxing, hashing, pointer-identity, gc]
requires: []
runtime: "<10s"
---

# 场景

把 int64、string、可比较 struct、`*ikPtrKey` 四类键混装进 `map[any]*ikVal`
（每类 750 项）。value 持有该装箱键、变长 body 与同 kind 的链指针。建表后 GC，
用新装箱的等值键查找；覆盖 1/3、删除 1/4 后再 GC；整表丢弃后旧值仍由
`oldVals` 持有；最后用相同键重建表并再次查找。各阶段穿插垃圾分配与显式 GC。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_map_iface_key_boxing`；
2. 建表后 map 长度 3000，四类键用新装箱的等值键均可命中；
3. GC 后每个值的 kind/idx/name/body 与种子推导值一致，value 内装箱键相等；
4. 覆盖项为更新版本，未覆盖项保持 ver=1，被删除项确实不存在；
5. 被覆盖前的旧值指针在后续 GC 与整表丢弃后仍可校验；
6. 重建 map 后所有键再次命中，指针键要求同一指针身份；
7. 不同 kind 的键不会互相碰撞，链指针在 GC 后仍可达。

## 失败判读

- `lookup ... failed`：interface 键哈希/相等性错误，或键被 GC 破坏；
- `boxed key identity lost`：value 中保存的装箱键被重新分配/篡改；
- `deleted ... still present` / `name=`：delete/update 语义或 map 桶状态错误；
- `old-after-drop`：仍被外部引用的旧值被提前回收（UAF）；
- `freshly boxed ... missed`：新建 interface 键无法与旧键相等，说明装箱/哈希路径异常。

## 参数

- `UAF_SEED`：决定 string/struct 键的 tag 与 body 内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 3000 项。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_map_iface_key_boxing/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
