---
id: l01_generic_bloom
dim: L01_generics
priority: P1
sources:
  - badger@v4.5.0: y/bloom_test.go:25 TestSmallBloomFilter 固定位串期望，hello/world 命中而 x/foo 未命中
  - badger@v4.5.0: y/bloom_test.go:56 TestBloomFilter 1..10000 长度下所有插入 key 不得假阴性且位图长度受限
tags: [L01, generics, bloom-filter, bitset, double-hashing, false-positive, clone, union, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型布隆过滤器 `bloom[T comparable]`：`m = n * bitsPerKey` 位图，Kirsch-Mitzenmacher 双哈希 `h1 + i*h2`（k=7），哈希函数作为泛型参数传入，分别实例化 `bloom[int64]`、`bloom[string]`、`bloom[structKey]`。对应 badger `y.Filter` 的位图布局与“无假阴性”契约。

int64 部分插入 n 个连续 key，逐项验证无假阴性，并对未插入区间统计假阳性率；克隆后逐 key 与原过滤器结果比对；两个各用 20 bit/key 的不相交过滤器做 OR 并集，验证两边 key 均不丢，并检查形状不一致时并集返回错误。string 部分维护 key→`*bloomPayload` 表，显式 GC 后核对过滤器命中与 payload 内容；结构体 key 部分覆盖复合可比较类型的泛型实例化。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_bloom`；
2. 所有已插入 key 的 `has` 必须为 true（任何假阴性即失败）；
3. 未插入 key 的假阳性率低于设定上界（单过滤器 ≤5%，并集 ≤5%）；
4. 位图置位数 >0 且 ≤ 插入数 × k；
5. 克隆过滤器与原过滤器对 2n 个 key 的判定完全一致；
6. 并集对两个输入集合的全部 key 无假阴性；形状不同的并集必须返回错误；
7. 字符串/结构体 key 在两次显式 GC 后仍正确命中，payload 指针与 blob 未损坏。

# 失败判读

- `false negative`：位图被错误回收、哈希位置越界或并集/克隆写坏了位；
- `false positive too high`：双哈希退化为单哈希或 m/k 计算错误；
- `clone divergence` / `union lost ... key`：位图切片复制或 OR 逻辑错误；
- `payload corrupted after GC`：过滤器与记录表之间的指针未保活（偏 L18 写屏障/扫描）；
- `SIGSEGV` / `fatal error: found bad pointer`：栈映射漏标或写屏障缺失。

# 参数

- `UAF_SEED`：决定 n（3000+seed%500）、string 规模与结构体集合内容，失败输出带种子；
- 无 TORTURE 参数（P1 固定规模，单次 <1s）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_bloom/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
