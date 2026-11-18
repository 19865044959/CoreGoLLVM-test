---
id: l11_bce_multidim
dim: L11_compiler_opts
priority: P2
sources:
  - gorm@v1.30.0: tests/migrate_test.go:1358 NestedIntArray [][]int64 `type:integer[3][3]` 嵌套数组模型
  - gorm@v1.30.0: clause/values_test.go:21 批量 INSERT 的二维 Values [][]interface{}
  - echo@v4.13.4: router_test.go:951 TestRouteMultiLevelBacktracking 多层路由节点的嵌套索引回溯
tags: [bounds-check-elimination, multi-dimensional, matrix, jagged, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 8 轮；TORTURE_ROUNDS=50 时 400 轮）"
---

# 场景

模拟批量写入与路由回溯中的多维遍历：每轮按 `UAF_SEED` 构造 24..32 行 × 20..30 列的二维矩阵、行长度递增的锯齿切片与 4×5×6 三维张量，分别用嵌套下标、`range`、列优先与子切片视图求和；再用独立条件判断的参考实现对照内层子切片求和。矩阵保留到全部轮次结束后跨 GC 复核。轮数由 `TORTURE_ROUNDS` 放大（上限 50 倍）。

# 期望行为

1. 行优先嵌套下标、`range`、列优先三种求和相等；
2. `m[i][1:len-1]` 内层子切片和等于逐元素条件判断的参考和；
3. 首尾角元素按 `((i*cols+j)%97)+salt` 公式正确，GC 后不变；
4. 锯齿切片每行长度 `i+4`，总和等于闭式公式；
5. 三维张量嵌套求和与扁平 `range` 求和相等；
6. 越界行下标、越界列下标、负下标、超出 len 的子切片、low>high 子切片全部 panic 且文本含 `out of range`，recover 后矩阵不变；
7. 保留矩阵在全部轮次与最终 GC 后求和不变；
8. 最后一行输出 `ALL PASS l11_bce_multidim`。

# 失败判读

- `row-major nested vs range` / `row-major vs column-major`：多维边界检查消除改变了合法访问结果；
- `interior subslice vs reference`：内层切片边界计算错误；
- `expected panic` / `unexpected panic text`：越界访问未 panic 或 panic 来源错误；
- `matrix sum after GC`：矩阵底层数组被回收/破坏；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定矩阵行列数与 salt，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（8 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_bce_multidim/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_bce_multidim/*.go
UAF_SEED=1 /tmp/case_n
```
