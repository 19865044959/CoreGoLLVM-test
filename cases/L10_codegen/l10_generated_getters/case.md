---
id: l10_generated_getters
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: proto/oneof_get_test.go:150 Opaque 生成消息访问器与零值语义
  - protobuf-go@v1.36.6: proto/reset_test.go:15 生成消息 Reset() 后复用
tags: [L10, L06, opaque-accessors, presence, nil-receiver, reset, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 protobuf opaque API 的生成方法集：结构体字段全部不可导出，标量字段用指针保存 presence，配 10 组 Get/Set/Has/Clear + 容器 getter + `Reset`。验证显式零值与未设置可区分、setter 在循环中产生独立指针、nil receiver getter 安全、GC 后指针字段/切片/map/嵌套结构体全部可读，以及 Reset 后复用。

## 期望行为

1. 零值对象的 getter 返回默认值且 Has 全为 false；
2. `SetID(0)`/`SetName("")`/`SetActive(false)` 后 Has 为 true，Get 仍返回零值；
3. 全字段赋值 → GC → 读取：标量、tags、profile（含 blob）、attrs、peers 内容一致；
4. 循环 Set 产生互不相同的指针，GC 后各指针值仍对应设置时的值；
5. nil receiver 的 Get/Has/容器 getter 安全返回零值，setter panic（被 recover 捕获）；
6. 200 轮 `UAF_SEED` 随机字段组合在 GC 后逐字段断言，Clear 后回到默认值与 false presence；
7. `Reset()` 清空全部业务字段，之后可再次赋值；
8. 最后一行 `ALL PASS l10_generated_getters`。

## 失败判读

- Has 与 Get 不一致：presence 指针未按 setter 语义维护；
- 循环 Set 后值串：指针复用同一循环变量地址（本用例本身会检测）；
- GC 后乱码/崩溃：指针字段或容器底层数组未正确保活；
- nil receiver panic：getter 缺少 `a != nil` 判断（setter 的 panic 是期望行为）；
- Reset 后残留：重置未覆盖全部字段。

## 参数

- `UAF_SEED`：随机字段组合与取值种子，失败输出带 seed。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_generated_getters/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
