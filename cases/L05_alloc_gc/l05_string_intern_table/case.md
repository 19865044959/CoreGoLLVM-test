---
id: l05_string_intern_table
dim: L05_alloc_gc
priority: P1
sources:
  - sonic@v1.13.2: internal/decoder/api/norace_test.go:33 的 TestStringReferring：解码字符串别名输入与显式拷贝两种模式
  - fasthttp@v1.59.0: args_test.go:150 的 TestArgsPeekMulti：返回值共享底层存储的子切片
  - goccy/go-json@v0.10.5: path_test.go:12 的 TestExtractPath：按路径抽取并共享原始 JSON 片段
tags: [L05, string-intern, shared-backing, dedupe, map, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟 JSON 字段名/metric 标签的字符串 intern：对大量内容重复但分别分配的字符串
做内容去重，使相同内容共享同一底层存储。用 `unsafe.StringData` 校验身份，
丢弃 intern 表后仅由 token 持有，显式 GC + 垃圾分配后共享关系与内容不变；
另含 8 个 4KB 大字符串的 intern 路径。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_string_intern_table`；
2. 16384 个 token 只对应 1536 个唯一词条，`len(table)==1536`；
3. 每个 token 的内容正确，且 `unsafe.StringData(token)` 等于其唯一词条的规范存储；
4. 1536 个不同词条的底层存储指针互不相同；
5. 丢弃 intern 表并 GC 后，token 仍保持内容与共享身份（共享由 token 自身维持）；
6. 512 个 4KB 大字符串 token 去重为 8 份，表丢弃后抽查内容完整；
7. 只保留少量 token 时，其余存储可回收且保留项仍正确共享。

## 失败判读

- `did not intern to canonical storage`：去重未生效或返回了临时拷贝；
- `distinct vocab ... shares data`：错误地合并了不同内容；
- `sharing lost after table drop`：规范字符串仅由表持有，GC 后悬垂；
- `content mismatch/lost`：字符串底层存储被提前回收或改写。

## 参数

- `UAF_SEED`：决定 token 到词条的选择序列（仍覆盖全部唯一词条）；失败输出带种子；
- 无其他 TORTURE 参数；固定 16K token + 512 大 token。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_string_intern_table/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
