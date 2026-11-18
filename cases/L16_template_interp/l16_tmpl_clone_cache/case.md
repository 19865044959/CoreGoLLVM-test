---
id: l16_tmpl_clone_cache
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:184 Clone 后追加的 FuncMap 仍生效
  - hugo@v0.155.0: tpl/internal/go_templates/htmltemplate/clone_test.go:205 Clone 与 Execute 并发执行
  - gitea@v1.23.5: modules/templates/scopedtmpl/scopedtmpl_test.go:17 并发执行器通过 Clone 隔离 FuncMap
tags: [L16, templates, clone, template-cache, FuncMap, option-isolation, concurrency, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

模拟站点渲染的模板缓存：解析一次 `layout/head/body/miss` 组合模板后按名缓存并反复执行；`Clone` 出独立命名空间，在 clone 上追加 FuncMap、新关联模板与 `AddParseTree`，验证 base 不可见；确认 Clone 不继承 base 的 `missingkey=error` 且后续 `Option` 只影响自身。另做共享模板 4 goroutine 并发执行与 4 个独立 clone 的并发闭包渲染。对应 Hugo 的 Clone/FuncMap 与 gitea 的 scoped executor 隔离。

# 期望行为

1. 一次解析多次执行 `layout` 输出正确；`Templates()` 名字排序为 `base,body,head,layout,miss`；缺失模板 `Lookup` 为 nil；
2. clone 执行结果与 base 相同；clone 追加 `bang` 函数与新模板 `extra` 后输出 `E:!go`，base 对 `extra` 报 `no template`，用 `bang` 解析报 `function "bang" not defined`；
3. clone 默认 `missingkey` 行为输出 `M:<no value>`；显式 `missingkey=error` 后报 `map has no entry`，base 始终保持 error 行为；
4. `AddParseTree("part")` 在 clone 上可执行 `P:7`，base 不可见；
5. 缓存 301 次查询中 miss=3、命中时返回同一 `*Template` 指针；
6. 4 goroutine × 20 次共享模板执行、4 clone × 20 次带闭包标签的执行全部输出正确；
7. `TORTURE_ROUNDS`（默认 24）轮随机顺序 clone/扩展/执行/缓存命中，每 4 轮 GC；GC 后 base、clone 的 FuncMap、闭包标签与缓存指针仍有效；
8. 末行输出 `ALL PASS l16_tmpl_clone_cache`。

# 失败判读

- `FAIL: base must not see ...`：Clone/New/AddParseTree 命名空间未隔离；
- `FAIL: clone2 ... missingkey`：Option 继承或隔离语义与预期不符；
- `FAIL: concurrent ...`：并发执行共享模板出现数据竞争或输出错乱；
- `FAIL: round N ...`：TORTURE 轮次中 clone/缓存状态被污染；
- GC 后失败：闭包捕获的标签或模板树被过早回收。

# 参数

- `TORTURE_ROUNDS`：默认 24，放大做长压测（上限 50000）；
- `UAF_SEED`：决定每轮变体执行顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_tmpl_clone_cache/*.go
TORTURE_ROUNDS=200 UAF_SEED=3 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
