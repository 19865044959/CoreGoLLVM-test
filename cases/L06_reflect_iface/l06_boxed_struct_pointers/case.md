---
id: l06_boxed_struct_pointers
dim: L06_reflect_iface
priority: P0
sources:
  - gorm@v1.30.0: schema/schema_test.go:38 指针/双指针/切片指针字段模型及其复用
  - frp@v0.67.0: pkg/config/v1/proxy_test.go:24 type 字段映射到具体实现后的接口断言
tags: [L06, interface-boxing, struct-value, pointer-fields, GC]
requires: []
runtime: "<3s"
---

# 场景

把带 `*node`/`*blob`/`*int64` 指针字段的 `payload` 值装箱进 `any` 与 `fmt.Stringer`，把链表头节点值装箱进全局接口后丢弃原头指针，仅靠接口数据区里的 `Next` 指针在多次 GC 后走完整条链。验证值拷贝语义与指针字段共享语义。

# 期望行为

1. 按值断言得到 payload，标量字段正确，指针链 11→12、64 字节数组、*int64=77 均可解引用；
2. 修改原指针指向的数据后，重新断言得到的新副本能看到变化，但 struct 标量 Name 保持装箱时快照；
3. 装箱值满足 `fmt.Stringer`，装箱指针满足自定义 `describer`；
4. 仅装箱链表头节点值、丢弃原头指针并 GC 后，仍能沿 Next 走出 9..12 个节点且求和正确；
5. 最后一行 `ALL PASS l06_boxed_struct_pointers`。

# 失败判读

- `boxed struct pointer chain` / `boxed struct array pointer`：接口数据区的指针字段未被扫描，对象被提前回收；
- `struct value copied into interface`：装箱语义错误（误用指针）；
- `boxed head chain values survive GC`：仅由接口内结构体值的指针字段维持的链在 GC 后断裂，典型漏标；
- 崩溃：接口数据区按错误类型元数据扫描。

# 参数

- `UAF_SEED`：决定链表长度（8..12，runner 注入），失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_boxed_struct_pointers/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
