---
id: l18_old_interface_hot_swap
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1466（messageState 缓存的 interface 指针跨 Reset/GC 必须保留）
  - pgx@v5.7.4: pgtype/pgtype_test.go:527（类型化 encode plan 缓存导致 UUID 数组类型混淆）
tags: [L18, write-barrier, interface, itab, hot-swap, noinline, gc]
requires: []
runtime: "<3s"
---

# 场景

老 holder 含 interface 字段 `cur`、6 槽老 interface 数组 `hist` 和 `interface{}` 字段 `raw`。每轮按 3 种具体类型（`*alpha`/`*beta`/`*gamma`，其中 gamma 内含指向年轻 `*node` 的字段）热替换：

- noinline `setAlpha/setBeta/setGamma` 分配新对象并写入 `h.cur`；
- noinline `setRaw` 只分配新对象并写入 `h.raw`，返回后新对象仅由该老 interface 字段引用；
- noinline `pushHist` 把当前 interface 值拷入老 interface 数组，随后 GC。

GC 后通过接口方法（动态派发）校验类型、值与内部 payload；hist 每个槽位也重新走动态派发。

# 期望行为

1. 每轮 GC 后 `cur` 的动态类型、value 与 payload 正确；
2. `raw` 的类型断言成功，无 itab/类型元数据丢失；
3. hist 6 个槽位在 GC 后均可动态派发且 payload 正确；
4. 30 轮稳定；
5. 退出码 0，最后一行 `ALL PASS l18_old_interface_hot_swap`。

# 失败判读

- `dynamic type lost`：老 interface 字段/数组写入时漏写屏障，类型对象或数据对象被回收；
- `payload corrupted`：具体类型对象字段被破坏（clobberfree 下常见 0xde 值）；
- `nil interface`：interface 数据指针被清零；
- efence 下 SIGSEGV：确认动态派发访问到已 unmap 内存。

# 参数

无随机路径；固定 30 轮、3 种具体类型轮换。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_interface_hot_swap/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
