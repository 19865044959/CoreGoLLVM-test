---
id: l11_copy_elision
dim: L11_compiler_opts
priority: P1
sources:
  - gorm@v1.30.0: statement_test.go:11 1–8 层 Statement.clone() 后追加条件，原/副本互不污染
  - gorm@v1.30.0: tests/generics_test.go:772 5 goroutine 复用泛型查询链，Where 不得互相污染
tags: [struct-copy, pointer-field, aliasing, slice-copy, array-copy, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 ORM 语句克隆/查询链复制：16 个节点的链式结构，每个节点含标量字段（id/name/weight）与两个指针字段（`next *cnode`、`data *[4]int64`）。分别做四种拷贝：结构体值赋值、按值传参并按值返回、`[]cnode` 逐元素 copy、`[4]cnode` 数组赋值。校验 Go 语义：标量字段副本独立，指针字段按值复制（共享同一目标），通过副本修改 `data` 内容必须能被原对象看到；GC 后链关系与全部标量仍正确。

# 期望行为

1. `cp := *head` 后标量相等；修改 `cp.id/name/weight` 不影响原对象；
2. `cp.next == head.next`、`cp.data == head.data`（指针按值复制）；
3. `cp.data[0] = 4242` 后 `head.data[0] == 4242`（共享目标，非深拷贝）；
4. 函数按值返回的副本修改标量不影响原对象，指针字段仍与原始别名；
5. `[]cnode` copy 与 `[4]cnode` 数组赋值后，逐元素标量独立、指针字段共享；GC 后关系不变；
6. 链上 `next` 依次指向下一节点、尾部为 nil，GC 后 16 个节点的 id/name/weight 全部正确；
7. 最后一行输出 `ALL PASS l11_copy_elision`。

# 失败判读

- `original scalars changed through copy`：值拷贝被错误优化成引用语义；
- `pointer field must be copied as-is` / `lost next pointer`：指针字段拷贝丢失或被深拷贝；
- `pointer target is shared, not deep copied`：指针目标被错误复制（语义与 Go 规范不符）；
- `chain link corrupted after GC`：链式引用未被写屏障/GC 正确保留；
- `id/name/weight corrupted after GC`：对象被回收或字段被零化。

# 参数

- 无随机路径，不读取 `UAF_SEED`；
- 不依赖拷贝消除是否发生，只校验拷贝语义。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_copy_elision/*.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_copy_elision/*.go && /tmp/case0
```
