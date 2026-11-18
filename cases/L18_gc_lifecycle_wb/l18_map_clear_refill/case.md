---
id: l18_map_clear_refill
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - badger@v4.5.0: db_test.go:249（TestConcurrentWrite：并发写 map 后全量顺序校验）
  - nats-server@v2.11.0: server/client_test.go:1304（TestUnsubRace：退订 tombstone 与重新注册）
tags: [L18, write-barrier, map-clear, refill, old-map, noinline, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 8 轮；TORTURE_ROUNDS 上限 32）"
---

# 场景

全局老 `map[int]*mcNode` 初始含 256 项，经双 GC 晋升。每轮先由 noinline
`mcClear` 调用 `clear(map)` 清空整个老 map，GC 后断言长度为 0；再由 noinline
`mcRefill` 用 256 个全新年轻节点（每个带 kid 子对象）重新填满，GC 后按固定
key 顺序逐项校验长度、值指针、名称/版本、data 与 kid。

与 `triple_wb_delete_reinsert`（逐键 delete/insert）不同，本用例覆盖 `clear`
内建清空整张老 map 后原地复用桶结构、再批量写入年轻值的边界路径。

## 期望行为

1. `clear` 后跨 GC map 长度恰为 0，无残留键；
2. 重填后 map 长度为 256，所有 key 均存在；
3. 每个值的 id/name/ver/data 与 kid 子对象逐字节正确；
4. 每代重填对象跨 GC 存活，无上一代残留；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_map_clear_refill`。

## 失败判读

- `map len=... after clear`：clear 未清空桶或旧键未释放；
- `map len=... after refill`：重填时 map 写屏障/扩容异常；
- `value header corrupted`：值对象被提前回收；
- `kid corrupted`：嵌套指针字段未扫描或被复用覆盖。

## 参数

- `UAF_SEED`：节点数据模式与失败信息；
- `TORTURE_ROUNDS`：轮数（默认 8，上限 32）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_map_clear_refill/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=24 UAF_SEED=4 /tmp/case
```
