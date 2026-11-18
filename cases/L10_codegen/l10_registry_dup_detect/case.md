---
id: l10_registry_dup_detect
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: internal/filedesc/build_test.go:23 TestInit 描述符 init 注册与全字段覆盖
  - protobuf-go@v1.36.6: cmd/protoc-gen-go/testdata/gen_test.go:10 testdata 包 blank import 触发 init 注册
  - kitex@v0.13.0: pkg/stats/event_test.go:25 TestDefineNewEvent 重名返回 ErrDuplicated
  - grpc-go@v1.71.1: encoding/encoding_test.go:70 TestDuplicateCompressorRegister 同名注册覆盖语义
tags: [L10, L12, descriptor-registry, duplicate-detect, atomic-commit, conflict-sort, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机注册轮次"
---

# 场景

模拟由多个生成文件经 init 注册描述符的全局注册表：按文件 path、包名、message/enum full name 建索引；message 与 enum 共享 symbol 命名空间。`Register` 先做非法检查、重复文件检查、跨文件/文件内/跨种类冲突收集（冲突名去重并排序），全部通过后一次性提交，失败不留下任何符号。批量注册 40 个生成文件后，测试重复路径、跨文件冲突、文件内重复、跨种类冲突、非法描述符、调用方事后修改描述符与 GC 稳定性；`UAF_SEED` 随机注册用独立模型对账。

## 期望行为

1. 40 个文件（240 message、80 enum、320 symbol）全部注册；文件/符号列表排序且唯一；message/enum 命名空间互不串门；
2. 重复文件 path 返回 `ErrDuplicateFile`，新符号不注册；
3. 跨文件符号冲突返回 `ErrConflict`，冲突名列表准确，冲突文件中唯一的新符号也不注册（原子性）；
4. 文件内重复、message 与 enum 同名、与已注册 message 同名的 enum 均返回 `ErrConflict`；
5. 多冲突时错误中的名称去重并按字典序排序；
6. nil/空 path/空 package/nil message/空 message 名/nil enum/空 enum 名返回 `ErrInvalid`，注册表不变；
7. 注册后修改调用方 fileDesc/message/fields 不影响注册表；
8. GC 后文件数、message/enum 数、symbol 列表与描述符内容稳定；
9. `UAF_SEED` 随机注册（70×`TORTURE_ROUNDS` 轮，每轮 3–8 个文件，含重复路径/文件内重复/随机冲突）成功/失败结果、冲突名集合与独立模型逐项一致；保留注册表 GC 后计数稳定；
10. 最后一行 `ALL PASS l10_registry_dup_detect`。

## 失败判读

- 冲突未检出或冲突名不符：full name 拼接/命名空间/去重排序错误；
- 冲突后计数变化：校验与提交未分离，部分符号已写入；
- 重复文件仍合并符号：path 检查在提交之后或缺失；
- 注册后修改影响注册表：未复制切片/描述符；
- GC 后计数/内容变化：全局表或描述符未保活；
- 随机失败可凭 `seed=` 复现，按 round/file 定位。

## 参数

- `UAF_SEED`：随机文件/包/符号/冲突注入种子，失败输出带 seed；
- `TORTURE_ROUNDS`：随机注册轮次倍数（默认 1，上限 50）。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_registry_dup_detect/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=10 GODEBUG=clobberfree=1 /tmp/case
```
