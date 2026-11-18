---
id: l11_escape_analysis
dim: L11_compiler_opts
priority: P1
sources:
  - fasthttp@v1.59.0: allocation_test.go:10 ServeConn 处理 100 个请求 0 分配（逃逸/内联敏感）
  - fasthttp@v1.59.0: allocation_test.go:71 URI Parse 100 次 0 分配
  - klauspost-compress@v1.17.11: zstd/internal/xxhash/xxhash_test.go:138 Sum64/Digest 必须 0 分配，验证内联/逃逸
tags: [escape-analysis, closure, interface-boxing, heap, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 HTTP 解析与哈希库中“局部对象不逃逸 / 返回指针逃逸 / 闭包捕获逃逸 / 接口装箱逃逸”四种分配语义。不逃逸路径只在函数内构造切片并按 `n^2` 公式求和；逃逸路径把 `*payload`、`any` 装箱值、`[]int64`、闭包和指向闭包环境的调用分别挂在全局切片上。`UAF_SEED` 在 16..96 间选择不逃逸切片长度，40 轮对象创建穿插 GC，最后在 GC 后逐项校验指针字段、数组、闭包结果与接口类型断言。

# 期望行为

1. 不逃逸局部切片求和等于 `n^2`，GC 前后一致；
2. 40 个 `*payload` 在 GC 后非 nil，`v/name/arr[0..3]` 全部正确；
3. 40 个装箱 `payload` 值类型断言成功，字段在 GC 后不变；
4. 40 个返回的 `[]int64` 长度按 `8+i%5`，末元素 `(len-1)^2` 正确；
5. 40 个闭包在 GC 后调用得到 `base+30`，捕获局部切片的闭包得到 `5+local[3]`；
6. 通过 interface 保存的 `*payload` 在 GC 后与原始指针相等且字段正确；
7. 最后一行输出 `ALL PASS l11_escape_analysis`。

# 失败判读

- `escaped payload ... corrupted`：逃逸对象被错误地放在栈上或提前回收；
- `boxed value type assertion failed` / `boxed value corrupted`：接口装箱数据布局或 GC 扫描错误；
- `closure ... corrupted`：闭包环境未逃逸/被破坏；
- `non-escaping sum ... mismatch`：局部切片计算本身错误（与逃逸无关）；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定不逃逸切片长度（16..96），不影响逃逸对象数量；
- 无 `TORTURE_ROUNDS`（P1，固定 40 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_escape_analysis/*.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case0 ./cases/L11_compiler_opts/l11_escape_analysis/*.go && UAF_SEED=7 /tmp/case0
```
