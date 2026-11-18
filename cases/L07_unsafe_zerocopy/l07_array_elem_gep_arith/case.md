---
id: l07_array_elem_gep_arith
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 TestDumpOpcode 依赖 unsafe 结构布局与指针偏移
  - sonic@v1.13.2: ast/buffer_test.go:42 节点块/元素链表按步长遍历，元素指针跨分配存活
tags: [L07, unsafe, pointer-arithmetic, GEP, arrays, interior-pointer]
requires: []
runtime: "<10s"
---

# 场景

仿零拷贝解析器/节点池：用 `unsafe.Add` 以 `unsafe.Sizeof(elem)` 为步长在堆数组上做元素指针算术（GEP），覆盖前向、后向、单表达式 uintptr 偏移、多维数组展平下标、元素内指针字段槽位（`&elem.link`）以及只保留派生指针跨 GC。元素含 string 与 `*node`，用于验证数组扫描位图。

# 期望行为

1. 前向 GEP 得到的 `*elem` 与 `&arr[i]` 相等，字段（id/name/link/flag）正确；
2. 从末元素减去 3 个步长得到 `&arr[last-3]`；
3. 单表达式 `unsafe.Pointer(uintptr(base) + k*sizeof)` 等于 `&arr[k]`，uintptr 不跨安全点保存；
4. 元素内 `**node` 槽位指针在 GC 后解引用得到原 `*node`，其 v/tag 正确；
5. `[6][5]elem` 展平下标 `r*5+c` 的 GEP 等于 `&grid[r][c]`；
6. 丢弃数组基址变量、只保留派生元素指针与槽位指针后，显式 GC + 垃圾分配不影响任何元素及其 link；
7. 24 轮新建/丢弃数组，每轮 5 个派生指针在 GC 后指向各自数组的正确元素；
8. 最后一行 `ALL PASS l07_array_elem_gep_arith`。

# 失败判读

- GEP 地址不等于 `&arr[i]`：元素宽度/步长计算错误，或负偏移方向错误；
- GC 后元素内容错乱/nil link：内部指针未被当作整对象根，或数组元素扫描位图漏标指针字段；
- 槽位解引用 SIGSEGV：`**node` 槽位本身未保活数组；
- 仅 clobberfree/efence 失败：对象被提前回收（UAF）；
- 仅 stress/k8s 失败：GOGC=1 并发标记期间的派生指针问题。

# 参数

- `UAF_SEED`：决定数组前缀长度（16..23）、后向偏移、uintptr 目标下标、槽位元素下标与多维对角线起点；失败信息带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_array_elem_gep_arith/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
