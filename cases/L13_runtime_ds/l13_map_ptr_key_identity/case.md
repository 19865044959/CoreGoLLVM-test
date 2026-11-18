---
id: l13_map_ptr_key_identity
dim: L13_runtime_ds
priority: P2
sources:
  - etcd@v3.5.0: server/mvcc/watchable_store_test.go:458 `wwe map[*watcher][]mvccpb.Event` 以 watcher 指针作 map key
  - echo@v4.13.4: middleware/middleware_test.go:76 `rules := map[*regexp.Regexp]string` 用编译后的正则指针索引路由
  - gitea@v1.23.5: models/user/user_test.go:496 `kases := map[*user_model.User]bool` 以用户对象指针集合判等
tags: [L13, map, pointer-key, identity, overwrite, delete, reinsert, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 2 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟注册表/监听表以对象指针作为 map key 的真实路径（etcd watcher 集合、echo 正则路由表、gitea 用户对象集合）。key 为堆上的 `*pkNode`，并额外分配字段等值但地址不同的 twin 指针：

1. 建立 900 项 `map[*pkNode]*pkVal`，value 反向持有 key 指针，逐项验证 twin 不命中；
2. 每 4 项覆盖 value（key 不变，旧 value 指针必须被替换）；
3. 每 3 项 delete，GC 后确认原指针 key 不再命中，邻近 key 不受影响；
4. 重插被删项并扩容到 1900 项，全量校验；
5. `TORTURE_ROUNDS` 轮：按 `UAF_SEED` 打乱顺序删除约 1/5、GC、再重插，校验遍历时的 key/value 指针身份与聚合值。

每阶段显式 `runtime.GC()` 并制造垃圾；遍历只做计数/求和，不依赖 map 顺序。

# 期望行为

1. 字段等值但地址不同的 twin key 永不命中；删除的指针 key 不复活；
2. 覆盖后 value 指针身份正确，value 内反向 key 指针与 blob 内容有效；
3. 删除/重插/扩容后长度与遍历聚合值符合模型；
4. GC 后 key 指向对象与 value 内容均未被破坏；
5. 最后一行输出 `ALL PASS l13_map_ptr_key_identity`。

# 失败判读

- `twin key aliased`：指针 key 比较被错误退化为内容比较；
- `deleted key resurrected`：删除/桶迁移路径错误；
- `value mismatch` / `node fields corrupted`：map key 或 value 中的指针未被扫描（UAF）；
- `iteration aggregate` 不符：扩容/重插过程中键值丢失或重复。

# 参数

- `UAF_SEED`：节点内容 salt、twin 分配与校验顺序的种子，失败输出带该值；
- `TORTURE_ROUNDS`：增删重插轮数（默认 2）。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_ptr_key_identity/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
