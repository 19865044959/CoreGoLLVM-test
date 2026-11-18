---
id: l16_reflect_iter_value
dim: L16_template_interp
priority: P1
sources:
  - echo@v4.13.4: echo_test.go:1221 reflect.ValueOf 构造动态处理器参数
  - echo@v4.13.4: echo_test.go:1226 reflect.ValueOf(...).MethodByName(...).Call 动态调用
  - ent@v0.14.4: entc/integration/integration_test.go:1792 reflect.ValueOf(updater).MethodByName 检查 setter 是否存在
  - badger@v4.5.0: options_test.go:63 reflect.ValueOf(&opt).Elem 读写结构体字段
tags: [L16, reflect, value-seq, seq2, map-iter, panic, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

模拟 echo/ent 在运行时按名字动态调用方法、gorm 类库用反射遍历容器：Go 1.24 的
`Value.Seq/Seq2` 把 slice/array/string/int/func/map 统一成迭代器，`SetIterKey/SetIterValue`
可在遍历 map 时免分配写入目标变量。本用例验证各种 Kind 的下标/键值语义、提前 break、
nil 容器、非法 Kind panic，以及这些反射值跨 GC 后的稳定性。

## 期望行为

1. slice、数组指针的 `Seq` 产出下标，用 `Index` 能取回原元素；
2. string 的 `Seq` 产出字节下标（`hé!` 为 0/1/3），int 产出 `0..n-1`，0/负数无迭代；
3. nil slice、nil map 不产生任何迭代；
4. func 的 `Seq` 执行 range-over-func，`break` 后迭代器停止；
5. map 的 `Seq2` 键值完整，排序后与直接 range 的结果一致；
6. `SetIterKey/SetIterValue` 写入的目标值与 `MapIter.Key/Value` 一致；
7. struct/float/invalid 的 `Seq`、int 的 `Seq2`、类型不匹配的 `SetIterKey` 均 panic；
8. 连续重复执行与 3 轮 GC 后输出摘要不变，map 遍历键集合仍确定；
9. GC 后修改 map 仍能被新的迭代器读到（4 个条目）。

## 失败判读

- `slice/array/string/int ... indices=`：`Seq` 语义与 Go 1.24 不符；
- `SetIterKey/Value=`：免分配写入路径错误或目标类型判断异常；
- `must panic`：非法 Kind 未按文档 panic，或 panic 恢复逻辑失效；
- `output changed`：反射值/容器在 GC 后失效，优先怀疑栈映射或对象存活；
- 超时：迭代器死循环（func Seq 未正确响应 break）。

## 参数

- `UAF_SEED`：控制每轮 GC 后 map 探针顺序；失败输出带种子；
- 无需其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L16_template_interp/l16_reflect_iter_value/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
