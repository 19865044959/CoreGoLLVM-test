---
id: l10_unknown_json_preserve
dim: L10_codegen
priority: P2
sources:
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:2540 DiscardUnknown 普通消息的未知字段策略
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:2570 DiscardUnknown 在嵌套 map 消息中的未知字段
  - protobuf-go@v1.36.6: encoding/protojson/decode_test.go:2641 宽松模式下未知枚举名的处理
  - sqlc@v1.28.0: internal/config/config_test.go:26 严格配置解码拒绝未知字段
tags: [L10, L06, json, unknown-fields, canonicalization, presence, map, nested, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机 JSON 对象轮次"
---

# 场景

模拟生成式消息的 JSON 解码器对未知对象字段的三种策略：`strict`（报错）、`discard`（丢弃）、`preserve`（保留并按规范化 JSON 重新输出）。已知字段包含标量、`tags []string`、`scores map[string]int32` 与嵌套 `child`；未知字段可出现在顶层与嵌套对象内。`preserve` 会递归排序对象键、保留数组顺序、压缩数字，从而把不同键序的输入归一为确定输出，并在 merge/reset 与输入缓冲释放后仍持有独立副本。

## 期望行为

1. 嵌套未知对象的键按字典序递归规范化（如 `{"b":...,"a":...}` → `{"a":...,"b":...}`），数组元素顺序不变；
2. `strict` 对顶层和嵌套未知字段都返回包含字段名的错误；`discard` 解码成功且再编码不包含未知字段；
3. `preserve` 固定输入的金标输出逐字节正确（已知字段固定顺序、map 的 key 排序、未知字段排序）；
4. 未知值在输入 buffer 被覆写并两轮 `runtime.GC()` 后仍可原样再编码；
5. 重复未知 key last-wins，重复已知 key last-wins；
6. `null`、缺失、空容器三者语义可区分：缺失时字段省略，`null` 保持 present-nil 并输出 `null`，空容器输出 `[]`/`{}`；
7. 显式零值（`0`、`""`）与缺失可区分；
8. merge 覆盖 present 字段、未知字段按 key 覆盖且深拷贝；reset 清空全部状态；
9. 非法 JSON、空字段名、null 的数组成员/ map 值、int32 溢出、尾随数据均被拒绝；
10. `TORTURE_ROUNDS` 轮随机键序 JSON 对象满足 canonical 往返稳定性，若失败打印 `UAF_SEED`。

## 失败判读

- `FAIL: ...` 或退出码非 0：规范化规则、presence 或 buffer 独立性被破坏，按失败信息定位；
- `failed checks` 集中出现在 torture：随机键序的 canonical 输出不稳定，通常是未知字段未深拷贝或 map 遍历未排序；
- `SIGSEGV` / `fatal error: found bad pointer`：未知字段原始字节引用了已释放的输入缓冲区（应复制）；
- 超时：随机轮次异常放大或 GC 饥饿。

## 参数

- `UAF_SEED`：随机对象轮次的种子，失败输出带该值；
- `TORTURE_ROUNDS`：随机轮次数，默认 250。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_unknown_json_preserve/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=2000 /tmp/case
```
