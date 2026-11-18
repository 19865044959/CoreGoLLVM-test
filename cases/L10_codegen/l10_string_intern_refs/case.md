---
id: l10_string_intern_refs
dim: L10_codegen
priority: P2
sources:
  - sonic@v1.13.2: internal/rt/fastconv_test.go:60 TestFastConvTString 字符串池复用
  - sonic@v1.13.2: internal/rt/fastconv_test.go:135 不同容量字符串池的构造与复用
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:12 TestGoCamelCase 生成名规范化表
  - protobuf-go@v1.36.6: internal/strs/strings_test.go:67 TestName 枚举/字段名推导表
  - ent@v0.14.4: entc/gen/type_test.go:299 生成标识符合法化与去重
tags: [L10, L13, L05, string-table, interning, refcount, collision, freeze, tombstone, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "默认 <10s；TORTURE_ROUNDS 放大随机 acquire/release 轮次"
---

# 场景

生成代码的字符串驻留表：按内容相等分配稳定 ID，引用计数支持多个使用方共享；归零后条目从桶链与 ID 索引移除，但 ID 永久保留（不复用，避免陈旧 ID 指向新字符串）；冻结后只允许获取已有字符串。表使用可注入的弱哈希与 4 个桶，强制 200 个同长度字符串形成碰撞链；另测空串、内嵌 NUL、非法 UTF-8、8KB 长串的字节精确语义，以及输入缓冲区毒化 + GC 后驻留值仍存活。

## 期望行为

1. 不同底层数组生成的相同内容只分配一个 ID，refs=2；释放一次仍存活，释放归零后移除；
2. 归零后再次获取同一内容分配**新 ID**，`issued` 单调递增，总数=已发行未回收；
3. 弱哈希碰撞表中 200 项全部可查；释放偶数项后 liveness 正确；重新获取得到全新 ID；`issued=300`；
4. 快照按 ID 升序，修改快照内容不影响表；
5. 冻结后获取已有字符串成功、获取新字符串返回 frozen；释放仍可用；释放后再插新值仍被冻结拒绝；
6. 空串、`a\x00b`、`\xff\xfe\xfd`、8KB 全零串互不合并且字节精确；
7. 300 个不同字符串在源 buffer 被 `0xEE` 覆写并两轮 GC 后值不变，随后全部释放清空；
8. `TORTURE_ROUNDS` 轮随机 acquire/release/lookup 与独立 refcount 模型对账（live 数、issued 数、每个 ID 的字符串与引用数、排序快照），失败带 `UAF_SEED`。

## 失败判读

- `FAIL: ...`：引用计数、ID 复用策略、冻结或碰撞链处理错误；
- `snapshot not sorted`：快照未按 ID 排序（输出不确定）；
- `lookup id` / `live count`：表与模型失配，优先检查 release 删除桶链/ID 索引的逻辑；
- 毒化后字符串变化：驻留表未按内容持有副本（实现缺陷）；
- 超时：随机轮次异常放大。

## 参数

- `UAF_SEED`：随机操作序列种子；
- `TORTURE_ROUNDS`：随机操作数，默认 400。

## 运行

```bash
go build -o /tmp/case ./cases/L10_codegen/l10_string_intern_refs/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=5000 /tmp/case
```
