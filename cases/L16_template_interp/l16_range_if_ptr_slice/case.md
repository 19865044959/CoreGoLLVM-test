---
id: l16_range_if_ptr_slice
dim: L16_template_interp
priority: P1
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:605 range slice 与 else 分支
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:607 range 中 break 与 else 的交互
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:608 range 中 continue 跳过 nil 元素
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:618 range 双变量 $x,$y 遍历切片
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:598 with 对 typed nil 的判断
tags: [L16, templates, text-template, range, nil-pointer, if-with-else, break-continue, GC]
requires: [templates]
runtime: "<10s"
---

# 场景

`[]*row` 指针切片中混有 nil 元素、nil 分数指针与 nil 叶子指针；6 个模板分别覆盖 range+索引、if/else 链、with/else、continue、break 与 range-else。每轮重新分配带环指针（leaf.Owner 指回 row），执行模板后强制 GC 再执行一次，校验 nil 语义与指针链不被破坏。对应 Hugo 内嵌 text/template 的 range/with 用例。

# 期望行为

1. `(0:a=10)(1:NIL)(2:b=-)(3:c=30)(4:d=40)`：nil 行走 else，nil *int 走 `=-`；
2. with 分支输出 `[L1/root/a][leaf=nil][L3/root/c][L4/root/d]`，`$.Title` 在嵌套作用域仍指向根数据；
3. continue 跳过无 Score 的行得到 `acd`；break 在 Skip 行提前结束得到 `abc`；
4. nil 标签切片走 range-else 输出 `no-tags`，行切片走正常分支；
5. 每轮 GC 后 row/Score/Leaf/Owner 指针值不变，重复执行输出逐字节一致；
6. 最后一行 `ALL PASS l16_range_if_ptr_slice`。

# 失败判读

- `FAIL: ... output`：range/if/with 的 nil 真值语义或作用域解析错误；
- GC 后指针为空/字段错乱：数据对象被错误回收（栈映射/写屏障问题）；
- 解析顺序随机但输出不一致：模板集合注册表被破坏；
- 超时：模板执行死循环（不应发生）。

# 参数

- `UAF_SEED`：决定轮数（5–8）与模板解析/执行顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_range_if_ptr_slice/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 GODEBUG=efence=1 /tmp/case
```
