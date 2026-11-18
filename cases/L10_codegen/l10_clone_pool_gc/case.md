---
id: l10_clone_pool_gc
dim: L10_codegen
priority: P2
sources:
  - grpc-go@v1.71.1: mem/buffers_test.go:67 TestBuffer_NewBufferRefAndFree 引用计数 buffer 的释放时机
  - grpc-go@v1.71.1: mem/buffer_pool_test.go:51 TestBufferPoolClears 池化 buffer 复用前清零，GC 可发生在 Get/Put 之间
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493 TestUnsafeAssumptions 多轮 GC 不回收消息剩余内存的假设
  - kitex@v0.13.0: pkg/mem/span_test.go:87 TestSpanCache size-class 缓存复制与 GC 前后校验
tags: [L10, L05, L18, clone, sync.Pool, gc-lifecycle, alias, retention, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大克隆/GC 轮次"
---

# 场景

生成风格 `Clone`/`CloneInto` 配合 `sync.Pool` 临时缓冲：payload 通过池化 scratch 复制后返回独立切片，归还前写入 0xEE 毒化；`CloneInto` 复用目标 tags/index/kids 容量但替换全部内容。验证源释放后保留副本、池缓冲不被别名、GC 前后数据完整，并做多轮批量克隆/GC。

## 期望行为

1. 固定树 Clone 后 dump 相等且无共享 payload/tags/index/kids；空（非 nil）与 nil 容器语义保留，nil child 保留；
2. 源/副本双向修改隔离；payload 是复制而非别名；
3. 源释放、只保留副本及 payload/tags/child 视图，多轮 GC 后完整；
4. `CloneInto` 覆盖脏目标：map 清空后重建而非合并、kids 替换而非追加、旧 child/旧 payload 不再被引用、tags 容量可复用；
5. 500 次池化 cloneBytes 结果与输入隔离；输入改写不影响结果、池毒化字节不泄漏；nil/空切片语义正确；
6. `4×TORTURE_ROUNDS` 轮、每轮 60 棵树：克隆后丢弃源并 GC，保留副本（每 10 棵取 1）跨轮与二次克隆均与快照一致；
7. 160 个 `UAF_SEED` 随机树满足 Clone/CloneInto 相等、无共享、源修改隔离与周期性 GC 存活；
8. 最后一行 `ALL PASS l10_clone_pool_gc`。

## 失败判读

- 池毒化字节出现在结果：cloneBytes 别名到 scratch 缓冲；
- GC 后乱码/崩溃：副本未持有 payload/child 或池对象被误用；
- CloneInto 后长度翻倍/残留 key：目标未截断/清空；
- 旧目标仍被引用：复用容量时未替换元素；
- 空容器变 nil：复制判断用了 `len(x)`；
- 随机失败可凭 `seed=` 复现；
- `TORTURE_ROUNDS` 下不稳定：保留副本/快照状态错。

## 参数

- `UAF_SEED`：随机树/池载荷种子，失败输出带 seed；
- `TORTURE_ROUNDS`：批量克隆/GC 轮次倍数（默认 1，上限 100）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_clone_pool_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
