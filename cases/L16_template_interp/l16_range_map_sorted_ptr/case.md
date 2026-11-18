---
id: l16_range_map_sorted_ptr
dim: L16_template_interp
priority: P2
sources:
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:603 range slice/map、双变量与 break/continue
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:305 map 缺 key 输出 <no value>，在 if 中为假
  - hugo@v0.155.0: tpl/internal/go_templates/texttemplate/exec_test.go:299 `.U.V` 连续两层指针字段求值
  - gitea@v1.23.5: modules/templates/helper_test.go:68 nil/指针/slice/map 的模板真值矩阵
tags: [L16, templates, range, map-sort, pointer-slice, nil-pointer, missing-key, GC, TORTURE_ROUNDS]
requires: [templates]
runtime: "<30s（TORTURE_ROUNDS 放大）"
---

# 场景

模板同时遍历 `map[string]*node`、`map[int][]*node`（值为指针切片）、`map[string]map[int]*node` 与 `[]*node`（含 nil 元素与 `Next` 指针链）。map 的插入顺序由 `UAF_SEED` 打乱，但 text/template 必须对 string/int 基本键排序，保证输出逐字节确定；缺 key 输出 `<no value>`、`if/with` 为假、nil map 走 range-else。对应 Hugo 内嵌 text/template 的 range/map 缺 key 测试。

# 期望行为

1. `ByName` 按键 `a,b,c` 排序输出 `a=a#1,b=b#2,c=nil,`，nil 指针值走 else；
2. `ByInt` 按数值键 `2,10`（而非字典序）排序，内层 `[]*node` 保留索引与指针链；
3. 指针切片输出 `0=l1#1,1=nil,2=l2#2>l3#3,`，nil 元素不触发方法调用；
4. 缺 key 输出 `<no value>`，`if/with` 均为假；`range` nil map 输出 `empty`；
5. 嵌套 map 外层/内层键均排序，nil 内层值输出 `nil`；
6. 每轮执行后强制 GC 再执行一次，输出逐字节一致，map/slice 中的指针身份不变；
7. 最后一行 `ALL PASS l16_range_map_sorted_ptr`。

# 失败判读

- `output` 差异：map 键未排序、nil 指针真值语义或 range 索引错误；
- `map value identity wrong`：模板执行期间 map/slice 指针被破坏；
- GC 后 `map entry`/`pointer slice after GC` 失败：堆对象被错误回收（写屏障/栈映射问题）；
- 超时：range 实现退化或 nil 循环。

# 参数

- `TORTURE_ROUNDS`：默认 12 轮，可放大（上限 200000）；
- `UAF_SEED`：决定每轮 map 键的插入顺序，失败输出带该种子。

# 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_range_map_sorted_ptr/*.go
TORTURE_ROUNDS=200 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
