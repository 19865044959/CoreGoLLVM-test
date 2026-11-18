---
id: l01_generic_deep_clone
dim: L01_generics
priority: P1
sources:
  - gorm@v1.30.0: statement_test.go:11 Statement.clone() 逐层复制 1–8 层 WHERE 条件，两份条件不得互相污染
  - quic-go@v0.50.1: config_test.go:136 反射枚举 Config 字段并 Clone：函数字段按引用复制，指针/切片/map 深拷贝
  - gorm@v1.30.0: tests/preload_test.go:239 每次 Session 独立复制查询上下文，并发执行结果一致
tags: [L01, generics, deep-clone, aliasing-isolation, nil-empty-semantics, recursive-copy, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

用泛型 `store[V]`（`map[string]*V` + 有序 key 切片）与递归 `cloneNode` 实现深拷贝：
每个 record 带 Tags 切片、Attrs map 和 1–3 层 Child 链，其中部分 Tags/Attrs 为
nil、部分为空非 nil。克隆后逐层检查指针、切片/ map 独立性、nil/空语义，再变异
克隆侧并验证原件不受影响。对应 gorm Statement.clone 与 quic-go Config.Clone 的
深拷贝隔离语义。

# 期望行为

1. 克隆 store 的 key 集合、Order 序列与原件一致，store/record/child 每层指针均不同；
2. Tags 切片与 Attrs map 不与原件共享底层存储：变异克隆侧后原件内容不变；
3. nil 与空非 nil 的 Tags/Attrs 在克隆中保持相同语义；
4. Child 链深度与每层 ID/Name 正确，克隆链尾变异不影响原链尾；
5. 第二轮 GC 后原件与克隆的深层指针、链尾和 Order 仍独立且完整；
6. nil store 克隆为空 store（M 非 nil、Order nil），nil node 克隆为 nil；
7. `store[int64]` 第二实例化复用同一 cloneStore 代码，指针与值独立；
8. 失败打印 `FAIL: ...` 并退出 1；最后一行 `ALL PASS l01_generic_deep_clone`。

# 失败判读

- `clone shares tags backing array` / `clone shares attrs map`：浅拷贝别名泄漏；
- `record pointer was not cloned` / `child node pointer was not cloned`：递归克隆遗漏层；
- `nil tags not preserved`：nil/空语义被 make/append 改写；
- `deep clone shares tail node`：递归链尾被共享；
- `... corrupted after GC`：克隆对象指针未被扫描或写屏障丢失；
- `FAILED ... seed=`：失败输出带 UAF_SEED。

# 参数

- `UAF_SEED`：决定 record 数（28..40），失败输出带种子。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L01_generics/l01_generic_deep_clone/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
