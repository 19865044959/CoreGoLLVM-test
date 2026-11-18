---
id: l12_blank_import_register
dim: L12_init_link
priority: P1
sources:
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 注册编码实现并按名查找
  - go-redis@v9.8.0: example_test.go:18 init() 中构造并注册全局对象
tags: [L12, blank-identifier, package-init, registry, side-effect, concurrency, gc]
requires: []
runtime: "<10s"
---

# 场景

三个文件分别用 `var _ = register("alpha"|"beta"|"gamma", factor)` 在包级变量初始化阶段产生注册副作用，模拟同包多文件插件注册。注册函数对零值全局表做惰性建 map，因此不依赖文件顺序；init 阶段应看到完整注册表。主程序并发只读并穿插 GC。

# 期望行为

1. `register` 在 init 前恰好调用 3 次，init 观测到的表大小与计数均为 3；
2. 名字排序稳定为 `[alpha beta gamma]`，factor 分别为 3/5/7；
3. 每个 codec 的 16 字节 mark 按 `byte(factor+i)` 生成，总校验和 600；
4. `total(10)=150`、`total(2)=30`；
5. 8 goroutine 只读查询 + 主 goroutine GC 期间结果全部正确；
6. GC 后注册计数仍为 3，codec 指针与 mark 数据仍有效；
7. 最后一行 `ALL PASS l12_blank_import_register`。

# 失败判读

- init 观测数为 0/部分：`var _ = ...` 初始化副作用未执行或被文件顺序影响；
- 运行期注册表为空：包级变量初始化顺序错误（注册发生在 registry 初始化前）；
- 并发查询失败：注册后仍有写入（注册表应冻结）；
- GC 后 mark 损坏或指针变 nil：全局 map/指针扫描问题。

# 参数

- `UAF_SEED`：决定并发查询的起始 key 偏移；只影响遍历顺序，不影响断言结果，失败行会带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_blank_import_register/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
