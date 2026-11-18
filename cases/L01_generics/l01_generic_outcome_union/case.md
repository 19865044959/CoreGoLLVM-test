---
id: l01_generic_outcome_union
dim: L01_generics
priority: P0
sources:
  - pgx@v5.7.4: rows_test.go:112 ForEachRow 回调中途 abort，剩余行不再消费
  - pgx@v5.7.4: batch_test.go:484 批量结果只读 3 行后 Close，再读下一批结果仍正确
  - go-redis@v9.8.0: error_test.go:25 ShouldRetry 按 EOF/池超时/LOADING/READONLY 分类重试
tags: [L01, generics, tagged-union, outcome, retry, skip, fold, value-invariant, gc, UAF_SEED]
requires: [generics]
runtime: "<3s"
---

# 场景

泛型四态结果联合 `outcome[T any]`：OK/RETRY/SKIP/FAIL 标签 + 泛型负载指针 + 重试次数/原因/错误码。`Value()` 只对 OK 暴露负载，`Pending()` 只对 RETRY 暴露负载，SKIP/FAIL 不得伪装成功；`MapOutcome[T,U]`/`FoldOutcome[T,R]` 是泛型函数（Go 方法不支持自带类型参数），保留标签与元数据。对应批处理结果部分消费、可重试错误分类与跳过/失败终态。

构造 220–260 条混合结果并显式 GC，逐项校验标签不变量与负载指针；Map 到 string 结果并校验映射调用次数；Fold 四分支计数与求和；随后把 attempt<2 的 RETRY 收敛为 OK（复用同一负载指针）并 GC 复查；最后实例化 `outcome[int64]` 验证值型负载。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_outcome_union`；
2. OK 的 `Value()` 返回同一负载指针，RETRY 的 `Pending()` 返回同一指针；SKIP/FAIL 两者均不可用；
3. RETRY 不是终态，OK/SKIP/FAIL 是终态；四态计数之和等于 N；
4. MapOutcome 保留 tag/attempt/reason/code，只对 OK/RETRY 调用映射函数，GC 后新负载内容正确；
5. FoldOutcome 四分支调用次数与独立计数一致，总和与逐项复算一致；
6. 重试收敛后 OK 数量增加量为收敛数，未收敛 RETRY 仍保留指针；int64 实例 fold 正确；clobberfree 下一致。

# 失败判读

- `outcome ... lost value` / `exposed Value()`：标签与负载的联合约束被破坏，优先检查泛型构造器与接口转换；
- `MapOutcome changed tag` / 调用次数不符：泛型函数实例化后的分支选择异常；
- `fold ... mismatch`：分支计数或聚合错误；
- pointer changed：负载在 GC 后失效，怀疑栈映射/写屏障。

## 参数

- `UAF_SEED`：决定结果条数（220–260），标签分布按 `i%7` 固定。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_outcome_union/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
