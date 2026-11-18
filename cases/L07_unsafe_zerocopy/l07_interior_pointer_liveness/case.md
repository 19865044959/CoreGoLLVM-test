---
id: l07_interior_pointer_liveness
dim: L07_unsafe_zerocopy
priority: P0
sources:
  - fasthttp@v1.59.0: header_test.go:325 RawHeaders/Peek 返回缓冲子切片，视图与原始块并存
  - goccy-json@v0.10.5: path_test.go:12 抽取返回输入子切片，输入释放前视图必须有效
tags: [L07, unsafe, interior-pointer, slice-view, liveness]
requires: []
runtime: "<3s"
---

# 场景

零拷贝库把 `&arr[i]`、`unsafe.Slice(&arr[i], n)`、结构体字段指针或字节视图交给调用方后，往往不再保留指向数组基址的变量。GC 必须把内部指针视为整个堆对象（及其指针字段）的根。用例分别在丢弃 `*[32]cell` 基址、`*[4096]byte` 基址和 holder 指针后强制 GC，再通过内部指针/视图逐元素校验。

# 期望行为

1. 指向堆数组元素的 `*cell` 与从该元素起始的 slice 视图在 GC 后内容完整，元素内 `link` 指针仍指向有效 `node`；
2. 仅保留 `[]byte` 视图时，4096 字节内容在 GC 后不变，写视图仍可生效；
3. 仅保留 `&holder.arr[7]` 时，holder 整体不被回收，元素及其指针字段可读；
4. 最后一行 `ALL PASS l07_interior_pointer_liveness`。

# 失败判读

- clobberfree/efence 下内容被改写或 SIGSEGV：内部指针未让所在对象存活，或栈/全局位图漏标；
- 元素 `link` 为 nil/错乱：数组元素类型含指针时的扫描位图错误；
- 只在 stress/k8s（GOGC=1）下失败：并发标记期间派生指针未被视为根。

# 参数

- `UAF_SEED`：选择起始元素 `start=seed%8` 与视图长度 `12+seed%5`，失败信息带种子；
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_interior_pointer_liveness/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
