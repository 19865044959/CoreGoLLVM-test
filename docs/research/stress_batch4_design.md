# 第四批（y01–y10）设计：沿着 x05 命中点扩大返回槽/栈槽零化矩阵

> 背景：x05 在 CoreGoLLVM 上命中真实 UAF——“大对象 + 匿名（非命名）按值返回槽未清零，GC 扫到旧已释放数据”。
> 本批不重复已知形状，而是沿“结果槽/参数槽/临时槽未初始化 × 晚写入/被扫描”这一根因族，
> 把每个相邻变体各打满一轮，另补 stackObjectRecord/typedmemmove/gcprog 等仍未覆盖的 R 风险点。

| ID | 形状 | 为什么不重复 | 优先级 |
|---|---|---|---|
| y01 | 匿名返回大对象：`func f() Big` 无命名结果，调用方立刻消费前 GC；多调用点复用同一槽；脏栈 maker 递归铺垫 | x05 命中形状的**最小纯化+压力化**（x05 是 panic 载体混合形态；y01 专打槽本身，压力拉满：深度抖动+每轮 GC+分字段部分写入） | P0 |
| y02 | 多返回混合：`(Big, error)`、`(int, Big, *T)`、`(Big, Big2)` 多个大匿名结果并置；部分结果早消费、晚消费交错 | x05/g01/g03 单结果；多结果并置槽布局不同 | P0 |
| y03 | 泛型实例化返回：`Box[T].Get() T` 大值返回 × 8+ 实参类型（含指针字段 struct）；字典/单态化双路径 | x01 泛型只传参；返回侧未打 | P0 |
| y04 | RVO/内联消拷贝：返回局部聚合，调用方取字段地址逃逸；内联 vs noinline 双版本 × 返回后 GC | x03 命名+defer；D19 RVO 消拷贝路径未打 | P0 |
| y05 | 方法值/接口 wrapper 返回大值：`i.(interface).Method() Big`（itab 派发+wrapper 可能尾调用）；receiver 大值拷贝 | D20 尾调用 × 返回槽组合未打 | P0 |
| y06 | stackObjectRecord：显式 `//go:noinline` 大局部 + `defer` 取址 + 栈对象指针导出给 GC（逃逸分析标 addrtaken）→ 收缩/复制后复用 | A9 从未覆盖（survey §1 证实）；x05 的 dead object 是 panic 保活，这里是**正常流**中的 stack obj 复用 | P0 |
| y07 | typedmemmove/批量搬运：`copy(dstSlice, srcSlice)`、append、`m[k]=big` 值拷贝 × GC 落点穷举（搬运前后/中） | C17 GoTypedMemmove 从未打；n04 只打 `[]*node`（指针批量），y07 打**含指针的大值搬运** | P0 |
| y08 | 全局巨型 gcprog：`[1<<16]*T`、`chan [512]Kv`、map 值大数组 × 并发改写 × typelinks 压力 | B14/B15 issue1002 路径；x11 设计了未实现 | P1 |
| y09 | 参数槽不初始化：`f(small)` 内联后参数槽含脏值再取址导出；`defer f(arg)` 参数求值时点 × GC | A8 参数区边界；g 系列打的是 goret 结果槽，参数槽未打 | P1 |
| y10 | 组合轰炸：y01+y03+y05 三形态随机轮换 × 高频 GC × window profile | 终验混合 | P1 |
