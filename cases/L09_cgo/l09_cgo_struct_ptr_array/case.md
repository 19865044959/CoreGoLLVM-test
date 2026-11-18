---
id: l09_cgo_struct_ptr_array
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，C 结构体指针数组是常见句柄/邻接表形态）
  - docs/research/tailscale.md:68（cgo/内核边界需真实 C 路径验证）
tags: [L09, cgo, struct-pointer-array, double-pointer, unsafe.Slice, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库返回“结构体指针数组”（`node_t **`）的绑定形态，例如图邻接表、句柄数组、C 侧对象列表。C 用 `calloc` 分配指针数组，并为每个元素单独 `malloc` 一个 `node_t{id,weight,value,tag[8]}`；Go 用 `unsafe.Slice` 把 `node_t **` 转成 `[]*C.node_t`，直接读写字段，随后跨多轮显式 GC 复核，最后交还 C 统一 `free`。C 只持有自己分配的内存，不保留 Go 指针。

# 期望行为

1. 指针数组非 NULL、元素数与请求一致，每个元素非 NULL；
2. 每个节点的 `id/weight/value/tag` 与 C 侧填充规则一致，`tag[7]` 为 NUL；
3. C 侧求和与 Go 按同样规则计算的总和一致；
4. Go 通过指针数组和直接字段赋值修改后，C 求和立即反映新值；
5. 12 轮垃圾分配 + `runtime.GC()` 后，指针数组长度、节点内容、修改值均不变；
6. 第二组小数组可独立分配/释放；`free(NULL)` 路径安全；
7. 最后一行 `ALL PASS l09_cgo_struct_ptr_array`。

# 失败判读

- `FAIL: node[i] non-nil`：C 分配失败或指针数组越界；
- `FAIL: C sum ...`：Go 通过 `node_t **` 的间接访问错位，或字段布局与 C 不一致；
- GC 后内容变化/崩溃：Go 侧的 `[]*C.node_t` 失去 C 内存可见性，或 C 误存了 Go 指针（本用例 C 只接收 `&count` 且在调用期内写完即返回）；
- `SIGSEGV`：指针数组或节点已被提前 free，或 double-pointer 解引用层级错误。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`，`ptrarray.c`/`ptrarray.h` 与 `main.go` 同目录参与构建。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_struct_ptr_array
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
