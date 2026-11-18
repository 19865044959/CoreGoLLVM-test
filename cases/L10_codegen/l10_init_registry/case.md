---
id: l10_init_registry
dim: L10_codegen
priority: P1
sources:
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 testdata 包 blank import 注册 init
  - kitex@v0.13.0: pkg/stats/event_test.go:25 全局事件注册表：新增/重名/冻结
  - protobuf-go@v1.36.6: internal/filedesc/build_test.go:23 描述符 init 注册与全字段覆盖
tags: [L10, L12, init, registry, duplicate, freeze, sync.Once, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟生成代码的包级 init 注册表：多个 `init()` 按声明顺序把类型名 → 构造函数登记到全局 registry，保持插入顺序；重复注册返回 `ErrDuplicate` 且首个工厂生效，空名/nil 工厂返回 `ErrInvalid`，`freeze` 之后新注册返回 `ErrFrozen`。另模拟 protoc-gen-go 生成物的 file descriptor：`sync.Once` + 包级指针，多次调用返回同一对象。

## 期望行为

1. init 结束后注册顺序恰为 `user,post,comment,attachment,session`；
2. 每个注册项构造出的 entity 类型/字段表正确，Clone 返回新指针且内容相等；
3. 重复注册 `user` 返回 `ErrDuplicate`，顺序不变且首次工厂仍生效（Name=="anonymous"）；
4. 空类型名与 nil 工厂返回 `ErrInvalid` 且不进入注册表；未知类型返回 `ErrUnknown`；
5. freeze 后新注册返回 `ErrFrozen`，重复 freeze 幂等，已有项仍可查询；
6. GC 后注册顺序、工厂闭包、200 个已构造 entity 指针全部存活；
7. file descriptor 多次 `fileOrderProtoInit()` 返回同一指针，GC 后指针与 encoded 字节不变；
8. 最后一行 `ALL PASS l10_init_registry`。

## 失败判读

- 顺序不符：init 声明顺序或 registry 追加顺序错误；
- 重复注册覆盖首项：register 未先查重；
- freeze 后仍可注册：冻结标志未检查；
- GC 后 entity/描述符损坏：包级注册表未保活工厂闭包捕获的对象；
- 描述符指针变化：sync.Once 被绕过或重复构造。

## 参数

- `UAF_SEED`：本用例无随机路径，变量仅为统一契约保留。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_init_registry/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
