---
id: l11_dead_store_swap
dim: L11_compiler_opts
priority: P1
sources:
  - golangci-lint@v2.0.2: pkg/golinters/ineffassign/testdata/ineffassign.go:10 赋值后立即被覆盖的 ineffectual assignment
  - golangci-lint@v2.0.2: pkg/golinters/staticcheck/testdata/staticcheck.go:11 自赋值 x = x
  - gorm@v1.30.0: tests/hooks_test.go:227 钩子链上 Name += "_clone" 的字段修改
tags: [dead-store-elimination, parallel-assignment, swap, aliasing, side-effect, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 ORM 钩子链与 lint 死存储规则中的多赋值/交换：并行赋值 `x,y,z = f1(),f2(),f3()` 的求值顺序与副作用、blank LHS 仍求值、连续交换后中间值又被覆盖、结构体字段交换、切片反转、map 元素交换、数组元素通过别名改后读取，以及自赋值。无副作用的死交换可被消除，但 RHS 中的函数调用不能消失；所有最终值和被保留的切片在 GC 后必须不变。

# 期望行为

1. 并行赋值得到 10/20/30，副作用顺序严格为 1、2、3；
2. `_, x = f1(), f2()` 返回 20 且两个 RHS 都执行；
3. 两次交换后中间值被 7/9 覆盖，结果为 709，首次交换的 RHS 副作用仍为 1、2；
4. 结构体字段交换后为 21；
5. 64..95 元素切片反转正确，GC 后仍满足 `rev[i]==orig[n-1-i]`；
6. 自赋值、指针别名最后一次写、map 交换、数组别名交换结果正确；
7. 交换指针字段的对象在 GC 后字段与内部指针完整；
8. 循环内被覆盖的 store 最终值与选中中间值符合公式，GC 后重算一致；
9. 最后一行输出 `ALL PASS l11_dead_store_swap`。

# 失败判读

- `parallel assignment ...`：并行赋值结果或求值顺序错误；
- `blank LHS still evaluates RHS`：死 store 消除越过 RHS 调用；
- `overwritten swap RHS side effects`：被覆盖交换的副作用丢失；
- `alias sees final store` / `array element swap through alias`：别名可见的 store 被删除；
- `slice reversal after GC` / `swapped pointer fields after GC`：保留数据或对象被 GC 破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定反转切片长度 64..95，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_dead_store_swap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_dead_store_swap/*.go
UAF_SEED=1 /tmp/case_n
```
