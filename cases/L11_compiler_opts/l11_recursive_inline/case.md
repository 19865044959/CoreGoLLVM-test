---
id: l11_recursive_inline
dim: L11_compiler_opts
priority: P1
sources:
  - goccy-json@v0.10.5: decode_test.go:2835 TestUnmarshalMaxDepth 万层嵌套递归解析
  - gorm@v1.30.0: schema/schema_test.go:181 TestNestedModel 三层嵌入结构的递归字段收集
  - echo@v4.13.4: bind_test.go:177 TestToMultipleFields 嵌套结构体/tag 的递归反射遍历
tags: [inline, recursion, mutual-recursion, stack-growth, panic-recover, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 JSON 深度解析、ORM 模型展开与 Binder 递归遍历：同一求和工作分别由递归函数、`//go:noinline` 递归函数和迭代函数实现（Go 编译器不内联递归函数）；互递归 even/odd 校验奇偶；叶子包装函数会被内联进递归体，与迭代闭式公式对照；三函数调用链分别以普通与全 `//go:noinline` 版本对照。另构建深度 10..12 的二叉树（由 `UAF_SEED` 决定），在显式 GC 后复核递归/迭代求和、节点数与左脊柱，并在 5000 层递归底部触发 panic/recover。

# 期望行为

1. 递归、noinline 递归与迭代求和都等于 `n(n+1)/2`（n=9000+seed%1000）；
2. 互递归 even/odd 与 `n%2` 一致；
3. 被内联的叶子函数递归结果与迭代公式相同，n=0 时返回常量 3；
4. 普通调用链与 noinline 调用链对 6 组输入结果相同；
5. 二叉树递归/迭代求和一致，节点数等于 `2^(depth+1)-1`，左脊柱在 GC 后仍为叶节点；
6. 5000 层递归 panic 被 recover 为 `recovered:bottom`，深度 0 同样成立；
7. 所有结果在 GC 后保持一致；
8. 最后一行输出 `ALL PASS l11_recursive_inline`。

# 失败判读

- `deep recursion sum` / `noinline deep recursion sum`：递归内联边界处理改变语义或栈增长后数据错误；
- `mutual recursion`：互递归调用被错误优化；
- `recursive leaf packages`：内联进递归体的叶子函数结果错误；
- `inline chain vs noinline chain`：内联链与未内联链结果不一致；
- `tree ... after GC`：树节点被回收/字段被破坏，或递归遍历栈扫描错误；
- `recovered:bottom` 缺失：深递归 panic/recover 控制流被破坏。

# 参数

- `UAF_SEED`：决定线性递归深度 9000..9999 与树深 10..12，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定规模）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_recursive_inline/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_recursive_inline/*.go
UAF_SEED=1 /tmp/case_n
```
