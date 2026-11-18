---
id: l06_error_assert_matrix
dim: L06_reflect_iface
priority: P1
sources:
  - echo@v4.13.4: json_test.go:53 SyntaxError/UnmarshalTypeError 经 errors.As 提取位置
  - echo@v4.13.4: echo_test.go:1302 HTTPError 的 SetInternal/WithInternal/Unwrap 链
  - gin@v1.10.0: errors_test.go:115 两层 fmt.Errorf("%w") + errors.Is/As
tags: [L06, error, errors.As, errors.Is, errors.Join, unwrap-forest, typed-nil, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

定义普通指针 error、带自定义 `Is` 的值 error、Timeout 接口 error、单链 `Unwrap` 包装器与
`Unwrap() []error` 森林，用 `fmt.Errorf("%w")` 多层包裹并 `errors.Join` 混合。对 10 类错误 ×
6 类 As target（含 `*interface{Timeout}`）做矩阵断言；再覆盖 `*error/*any` target、直接断言只看
最外层、Join 森林遍历、typed nil、As target 非法时的 panic 语义。所有错误对象跨显式 GC 复用。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_error_assert_matrix`；
2. 10×6 的 `errors.As` 矩阵与预期完全一致（值 error 不能被 As 到其指针类型）；
3. `errors.Is` 穿过 fmt 包装与 Join 森林，自定义 Is 按 Code 匹配，typed nil 与自身相等；
4. `errors.Unwrap` 只解单链，Join/forest 的 `Unwrap() []error` 长度正确；
5. As target 非指针、nil 指针、指向非 error 类型均 panic 并被 recover；
6. error 接口方法与实现关系正确；GC 后所有解包结果与 payload 指针不变。

## 失败判读

- As 矩阵某项不符：错误链遍历/assignability 判定错误；
- Is 结果不符：包装链或自定义 Is 调用错误；
- Join/forest 长度或 Unwrap 行为错误：多错误展开实现错误；
- panic 语义不符：As target 校验缺失；
- GC 后 payload 丢失/崩溃：error 链中的指针未保活。

## 参数

- `UAF_SEED`：随机种子，决定包装层数与 Join 顺序；失败输出带出该值。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_error_assert_matrix/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
