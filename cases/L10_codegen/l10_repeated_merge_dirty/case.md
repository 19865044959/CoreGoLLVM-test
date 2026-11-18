---
id: l10_repeated_merge_dirty
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: proto/merge_test.go:320 Merge 对 repeated 字段追加
  - protobuf-go@v1.36.6: proto/merge_test.go:390 Merge 对 map 字段按键覆盖
  - protobuf-go@v1.36.6: proto/merge_test.go:477 Merge 对嵌套消息字段递归合并
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:1058 重复 map 键 last-wins
  - protobuf-go@v1.36.6: encoding/protowire/wire_test.go:349/408 长度前缀与字段边界
tags: [L10, L13, merge, dirty-destination, packed-repeated, map-entry, capacity-reuse, atomic-failure, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机合并轮次"
---

# 场景

生成式消息解码器把 repeated/map 字段解码进**已有脏数据**的目标对象（proto merge 语义）：repeated 追加、map 按键覆盖、单数嵌套消息递归合并、重复 map entry 整体替换。测试 packed 与 unpacked `repeated int32` 交错、空 packed 段、负数 int32 十字节 varint、map entry 缺 key/value、entry 内未知字段、重复键、零长度 bytes、目标 slice/map 预分配容量复用，以及输入缓冲毒化后的字符串/字节独立性。解码失败必须不修改目标对象。

## 期望行为

1. 固定构造的 payload 解码后 repeated 顺序为 `[5,6,7,-1]`，names/map/列表/child 均符合合并语义；
2. 空 packed 段不分配 repeated slice（保持 nil），零长度 bytes 解码为**非 nil 空**切片；
3. 重复 map 键整值替换而非按字段合并；entry 缺 key/value 使用零值；entry 内未知字段跳过；
4. `merge=true` 时 repeated 追加、map 键覆盖、旧键保留、child 递归合并；`merge=false` 忽略脏目标；
5. 合并复用目标 slice 容量（`cap` 不缩小）且不缩短已有元素；
6. 输入 buffer 被覆写并 GC 后，所有字符串/字节载荷仍正确；
7. 截断、错误 wire type、varint 溢出等错误返回后目标对象逐字节不变；
8. `TORTURE_ROUNDS` 轮随机消息 + 对抗性编码（packed/unpacked 交错、重复 entry、拆分 child）与独立模型逐字段一致；
9. 两个 payload 顺序合并的结果与一次性合并模型一致。

## 失败判读

- `FAIL: ...`：合并语义、nil/空区分或容量复用被破坏；
- 仅在 torture 失败：对抗性字段顺序或重复 entry 处理与模型不一致，失败信息带 `UAF_SEED`；
- `failed decode leaves destination unchanged` 失败：解码先写目标再校验，破坏了原子性；
- 毒化后数据错误：解码结果别名到输入 buffer；
- `SIGSEGV`：map/slice 复制时漏拷指针字段。

## 参数

- `UAF_SEED`：随机消息与对抗编码模式种子；
- `TORTURE_ROUNDS`：随机轮次数，默认 200。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_repeated_merge_dirty/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=2000 /tmp/case
```
