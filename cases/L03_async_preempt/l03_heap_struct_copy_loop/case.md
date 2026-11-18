---
id: l03_heap_struct_copy_loop
dim: L03_async_preempt
priority: P2
sources:
  - quic-go@v0.50.1: config_test.go:136 TestConfigCloning 深拷贝大配置结构，函数字段按引用、其余字段逐项复制
  - etcd@v3.5.0: server/etcdserver/server_test.go:1237 快照发送与应用循环并发交错，GC 插在快照拷贝前后
tags: [L03, async-preempt, large-object, heap-copy, typedmemmove, write-barrier, string-header, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

堆上 `hcBig` 约 64KiB（`[8192]int64` + `*hcNode` + string 头 + id）。每轮先在 noinline 的 `hcPrepare` 中把新分配的节点与字符串挂到 src，执行 `*dst = *src` 整体按值复制，然后清掉 src 侧引用，使 dst 成为新节点/新字符串头的唯一根；每 8 轮分配垃圾并显式 `runtime.GC()`，GC 后解引用 `dst.p`、核对 `dst.tag` 字符串内容与大数组采样值。每 32 轮重新分配 dst，旧 64KiB 大对象成为垃圾；另有并发 GC pump。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l03_heap_struct_copy_loop`；
2. 每轮复制后 `dst.p` 为 fresh 节点且字段完整，GC 后仍可解引用；
3. `dst.tag` 在 GC 后仍为轮次对应的字符串内容，`dst.id` 与小数组采样值一致；
4. 主循环显式 GC 次数等于 `ceil(rounds/8)`；
5. GC pump 至少完成 1 轮，结束前 src/dst 均存活；
6. 在 clobberfree/efence/stress/k8s 等 profile 下结果一致。

## 失败判读

- `FAIL: copied pointer survives GC`：大对象中的指针字段未被 typedmemmove/写屏障正确复制；
- `FAIL: copied string header survives GC`：string 头复制或指针位图问题；
- `FAIL: large array copy checksum`：大数组按值复制内容错误；
- `SIGSEGV` / `fatal error: found bad pointer`：疑似批量写屏障或栈映射漏标。

## 参数

- `UAF_SEED`：决定大数组基线值、变异位置步长/偏移；失败信息带出种子；
- `TORTURE_ROUNDS`：放大循环轮数（默认 512，上限 65536）。

## 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_heap_struct_copy_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4096 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
