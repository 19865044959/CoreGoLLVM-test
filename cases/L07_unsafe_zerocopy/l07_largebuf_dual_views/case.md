---
id: l07_largebuf_dual_views
dim: L07_unsafe_zerocopy
priority: P2
sources:
  - fasthttp@v1.59.0: header_test.go:325 RawHeaders 返回原始 header 块，key/value 视图共享同一缓冲
  - sonic@v1.13.2: ast/node_test.go:137 惰性节点保存输入子切片，Load 前不物化
  - pgx@v5.7.4: conn_test.go:1312 RawValues 底层内存在后续读取中被复用
tags: [L07, unsafe, large-buffer, slice-header, string-header, liveness]
requires: []
runtime: "<10s（TORTURE_ROUNDS 可放大）"
---

# 场景

大缓冲（1MiB 级）上的 slice/string 双 header 视图：手工构造 `{data,len,cap}` 切片头与 `{data,len}` string 头，在缓冲的头部、1/7、中点、尾部固定长度、最后一字节处派生视图；返回后缓冲只由这些视图保活，再显式 GC 并复核内容、校验和与别名关系。模拟 fasthttp RawHeaders、sonic 惰性 AST 节点等“大块输入 + 多个零拷贝窗口”的持有方式。

## 期望行为

1. 每轮 1MiB 级缓冲的 5 组 slice/string 视图长度、data 偏移精确，`unsafe.String` 与手工 header 指向同一地址；
2. `*sliceHeader` 回读的 data/len/cap 与构造值一致，视图内容校验和等于按偏移重算的期望值；
3. 尾部零长视图长度为 0，不触碰数据；
4. `deriveRound` 返回后缓冲变量不可达，显式 GC + 垃圾分配后所有视图内容、校验和、双 header 地址关系不变；
5. 多轮新建大缓冲，保留视图的底层地址互不相同（旧缓冲未被回收或复用）；
6. 放大 `TORTURE_ROUNDS` 后仍逐轮通过，末行 `ALL PASS l07_largebuf_dual_views`。

## 失败判读

- 校验和不符/内容错乱：header 偏移或 len 计算错误，或视图未让大缓冲存活（clobberfree 改写空闲内存）；
- GC 后 SIGSEGV：slice/string 头的 data 指针未被当作 GC 根；
- 不同轮次视图地址相同：旧大缓冲被提前回收，保留视图没有起到保活作用；
- 仅 stress/k8s 失败：并发标记期间的 header 视图存活问题；
- 超时：TORTURE_ROUNDS 过大或 GC 饥饿。

## 参数

- `UAF_SEED`：决定大缓冲长度（1MiB + 0..4095）与每轮内容模式，失败信息带种子；
- `TORTURE_ROUNDS`：轮数，默认 6；每轮 2 次显式 GC，放大可做分钟级压测。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_largebuf_dual_views/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=2000 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
