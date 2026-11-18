---
id: l11_iface_escape
dim: L11_compiler_opts
priority: P2
sources:
  - echo@v4.13.4: context_test.go:508 TestContextReset 池化 context 的 any store map 与 Reset
  - gorm@v1.30.0: schema/field_test.go:92 TestPointerFieldValuerAndSetter 指针字段反射 setter 路径
  - goccy-json@v0.10.5: decode_test.go:208 interface 解码子测试，值装箱为动态类型
tags: [escape-analysis, interface, interface-sink, boxing, type-assertion, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4 轮；TORTURE_ROUNDS=50 时 200 轮）"
---

# 场景

模拟请求上下文池、ORM 字段与 JSON 解码中“对象只经接口可见”的逃逸路径：每轮构造 24..47 个带指针链与数据切片的节点，分别经 `*[]any` 注入函数、`any`→`any` 包装函数、struct 的 `any` 字段、接口切片和 map 值保存；随后显式垃圾分配 + 两次 `runtime.GC()`，再通过类型断言、接口方法、指针链回读。另测 typed nil 指针装入接口后的非 nil 语义。轮数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 接口 sink 中每个节点在 GC 后可按序断言回 `*ieNode`，id 与数据切片和正确；
2. struct `any` 字段与接口字段指向的对象在 GC 后完整，接口方法结果正确；
3. 仅由接口值保活的指针链长度等于本轮节点数；
4. 包装函数返回的接口能看到原对象后续修改；
5. map 中接口值在 GC 后可读，删除偶数键后奇数键对象完整；
6. 接口切片动态分派求和等于公式；
7. typed nil 接口 `!= nil`，断言成功且得到 nil 指针；
8. 全部轮次的接口 sink 在最终 GC 后逐项匹配期望 id；
9. 保留的接口 holder 在最终 GC 后非 nil 且标签完整；
10. 最后一行输出 `ALL PASS l11_iface_escape`。

# 失败判读

- `interface sink values after GC` / `all interface-sunk nodes after final GC`：仅经接口引用的对象未被正确保活；
- `interface struct field after GC` / `retained interface holders`：接口字段或接口数据被回收/破坏；
- `wrapper interface observes mutation`：接口装箱复制了对象导致修改不可见；
- `interface slice dispatch` / `interface method through field`：itab 或接口方法派发错误；
- `typed nil interface`：接口 nil 判定与断言语义不符；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮节点数 24..47 与数据 salt，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（4 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_iface_escape/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_iface_escape/*.go
UAF_SEED=1 /tmp/case_n
```
