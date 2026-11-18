---
id: l11_method_value_alloc
dim: L11_compiler_opts
priority: P2
sources:
  - echo@v4.13.4: echo_test.go:1220 MethodByName.Call 与直接调用对照（方法值/反射调度）
  - gorm@v1.30.0: schema/callbacks_test.go:22 TestCallback 反射探测生命周期回调方法集
  - fasthttp@v1.59.0: stackless/func_test.go:10 any 参数 + 类型断言调用 4096 次
tags: [method-value, receiver-copy, pointer-receiver, method-expression, interface, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 4 轮；TORTURE_ROUNDS=50 时 200 轮）"
---

# 场景

模拟回调注册与中间件绑定中的方法值：值接收者方法值创建时复制接收者（含 8×int64 大结构体与字符串字段），指针接收者方法值共享对象；变量/指针重新赋值后旧方法值仍绑定旧接收者；切片元素与 map 中的方法值各自独立；接口上的方法值绑定当时的动态值与副本；方法表达式 `T.M`/`(*T).M` 与直接调用一致；nil 指针接收者的方法值合法。每轮批量创建值/指针方法值对，跨多轮与多次 GC 后调用仍保持各自的接收者状态。

# 期望行为

1. 值接收者方法值不受后续 `c.n` 修改影响，返回创建时的值；
2. 指针接收者方法值共享对象，连续调用累加正确；
3. 变量/指针重新赋值后，旧方法值仍作用于旧接收者；
4. 切片元素方法值在元素被修改后仍返回创建时的值；
5. map 中方法值返回创建时的值；
6. 方法表达式与直接调用结果一致，指针表达式修改原对象；
7. 大接收者的数组与标量字段在方法值创建时整体复制；
8. 字符串字段（tag）同样被复制；
9. 接口方法值绑定动态值与副本，不受后续修改影响；
10. nil 接收者的方法值调用返回 `-1`；
11. 批量方法值对在多次 GC 后：值方法值保持原值，指针方法值仍能修改各自接收者；
12. 最后一行输出 `ALL PASS l11_method_value_alloc`。

# 失败判读

- `value method value did not copy receiver` / `... followed variable reassignment`：值接收者未复制或绑定错对象；
- `pointer method value ...`：指针接收者未共享或被复制；
- `method expression ...`：方法表达式签名/接收者错误；
- `big value method value ...`：大结构体复制不完整；
- `retained ... corrupted after GC`：方法值闭包环境或接收者被回收；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮方法值对数量 `48..63`，失败输出带种子；
- `TORTURE_ROUNDS`：轮数倍数，默认 1（4 轮），上限 50。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_method_value_alloc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 UAF_SEED=3 GOGC=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_method_value_alloc/*.go
UAF_SEED=1 /tmp/case_n
```
