---
id: l05_iface_boxing_ring
dim: L05_alloc_gc
priority: P0
sources:
  - sonic@v1.13.2: ast/api_native_test.go:132 的 TestStackAny：//go:noinline 把值装箱成 any 后 GC/断言
  - fasthttp@v1.59.0: stackless/func_test.go:10 的 TestNewFuncSimple：any 参数 + 类型断言调用 4096 次
  - fasthttp@v1.59.0: fasthttpadaptor/adaptor_test.go:16 的 TestNewFastHTTPHandler：自定义 ctx 经接口适配与 hijack 接口断言
tags: [L05, interface-boxing, non-empty-interface, typed-nil, dispatch, gc]
requires: []
runtime: "<3s"
---

# 场景

把四类具体值（值接收者、指针接收者、含切片值、实现 error 的指针对应）装箱进
非空接口 `valuer`，连续 10 代覆盖 512 槽 ring，并在每代之后显式 GC，使旧装箱
对象成为垃圾；另用 map[string]valuer 保存混合装箱值。重点验证动态派发、
type switch/assert、接口环境中的指针，以及 typed-nil 的接口语义。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_iface_boxing_ring`；
2. 每代 512 个接口槽的 `Kind()/Value()` 与具体类型断言内容全部正确；
3. 每代 pin 的装箱值跨后续代覆盖 + GC 后仍完整；
4. map 中 512 个混合装箱值 GC 后仍能正确派发与断言；
5. typed-nil `*boxPtr` 赋给非空接口后接口非 nil，方法安全返回 `-1/"nilptr"`；
6. typed-nil `*boxErr` 作为 error 非 nil，`Error()` 返回 `nilerr`，且仍可转回 valuer；
7. typed-nil 指针放进空 interface 后非 nil，断言得到 nil 指针。

## 失败判读

- `slot ... corrupted`：接口数据字/type 指针被 GC 破坏，或装箱值底层存储被回收；
- `pinned ... corrupted`：旧装箱对象未被正确保活；
- `typed nil ... must be non-nil`：接口 typed-nil 语义错误；
- `method dispatch`：itab/方法集选择异常。

## 参数

- `UAF_SEED`：决定装箱值、切片长度与字节内容；失败输出带种子；
- 无其他 TORTURE 参数；固定 10 代 × 512 槽。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_iface_boxing_ring/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
