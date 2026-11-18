---
id: l12_var_dependency_order
dim: L12_init_link
priority: P0
sources:
  - go-redis@v9.8.0: bench_decode_test.go:21 包级 var initHello 在测试二进制启动时初始化
  - go-redis@v9.8.0: main_test.go:56 包级 cluster 变量在启动期构造 map/slice
tags: [L12, package-init, var-dependency-order, cross-file, gc]
requires: []
runtime: "<3s"
---

# 场景

包级变量依赖链分散在三个文件中（`c → b → a`、`base → derived1 → derived2`），其中引用者的文件名词法顺序反而在根变量之前。Go 规范要求按依赖序而非文件序初始化，本用例校验整条链的最终值、init 阶段可见性、结构体值拷贝语义与指针别名。

# 期望行为

1. `c=3`、`b=6`、`a=7`，即使 `a` 所在文件排在 `c` 之前；
2. `derived1=101`、`derived2=205`、`scaled=202`、`fnInit=208`（初始化表达式经过函数调用）；
3. 依赖变量为 key 的 map 字面量得到 `table[7]="seven"`、`table[101]="hundred-one"`；
4. `init()` 中一次性观察到链路总和 530；
5. GC + 垃圾分配后所有值不变；
6. 初始化期的 struct 值拷贝保持旧值，指针别名看到后续修改；
7. 最后一行 `ALL PASS l12_var_dependency_order`。

# 失败判读

- 某个依赖值为零值/旧值：包级变量初始化未按依赖序，或跨文件初始化排序错误；
- `init observed ...` 失败：init 早于包级变量初始化完成；
- GC 后才失败：全局变量数据段/指针扫描或写屏障问题；
- 编译失败提示初始化循环：依赖分析实现异常。

# 参数

无随机路径；`UAF_SEED` 不参与本用例。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_var_dependency_order/*.go
GODEBUG=clobberfree=1 /tmp/case
```
