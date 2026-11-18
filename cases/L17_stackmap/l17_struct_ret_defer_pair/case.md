---
id: l17_struct_ret_defer_pair
dim: L17_stackmap
priority: P1
sources:
  - echo@v4.13.4: echo_test.go:239（调用返回后把 response body 立即作为 defer 闭包实参保存）
  - gitea@v1.23.5: models/unit/unit_test.go:16（defer func 以多个按值快照参数清理全局状态）
tags: [L17, stackmap, struct-return, defer-record, multiple-defers, method-value, by-value-copy, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`makeResult` 按值返回含 `*item`、`[]*item`、`[2]*item`、`any` 与 string 的 struct。`run` 将该返回值同时注册为两个 defer 的按值实参（一个闭包、一个方法值 `checker.take`），随后原地重绑场内的指针字段、追加切片、替换 any 与 tag。defer 记录保存注册时的浅拷贝，内部各执行两轮 GC 并重算 checksum；返回的 struct 则携带 mutation 后的值。

`UAF_SEED` 决定每轮 base 与 struct 形态。

# 期望行为

1. 三个 defer 的 checksum 均等于注册时的原 struct checksum（方法值 defer 额外计入旧 receiver 的 keep 链）；
2. 方法值 defer 使用注册时的 receiver 副本，重绑定/修改 receiver 字段不影响它；
3. defer 执行顺序为 LIFO：`B`、`take`、`A`；
4. `run` 返回的 struct 在 caller GC 后保持 mutation 后的指针、切片与 any 字段；
5. 被重绑的旧对象只因 defer 记录存活，GC 后仍可完整解引用；
6. 退出码 0，最后一行 `ALL PASS l17_struct_ret_defer_pair`。

# 失败判读

- `deferred closure sums` / `deferred method sum`：defer 记录中的 struct 浅拷贝或 receiver 未被扫描；
- `deferred guard ran before return`：defer 提前执行或返回值快照错误；
- `defer LIFO order`：defer 链顺序异常；
- `returned p.v` / `returned ... lost`：按值返回槽未正确扫描；
- `mutation did not change`：用例自身构造错误。

# 参数

- `UAF_SEED`：每轮 base 种子，失败输出带该值；
- 固定 6 轮，struct 形态按 seed 在 nil/非 nil 切片、指针/字符串 any 间轮换。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_struct_ret_defer_pair/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
