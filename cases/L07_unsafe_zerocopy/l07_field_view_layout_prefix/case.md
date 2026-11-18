---
id: l07_field_view_layout_prefix
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 零拷贝重解释为 string
  - goccy-json@v0.10.5: internal/encoder/encode_opcode_test.go:8 unsafe 拆解结构体/接口头与字段偏移
  - yaegi@v0.16.1: internal/unsafe2/unsafe_test.go:10 reflect.StructOf 造结构体并改写字段类型
tags: [L07, unsafe, struct-field, prefix-layout, Offsetof, write-barrier]
requires: []
runtime: "<10s"
---

# 场景

字段零拷贝访问与前缀布局视图：`fExt` 以嵌入 `fBase` 开头，尾部还有 `extra [24]byte`、
`data []byte`、`tail *fLeaf`。用 `unsafe.Offsetof` 嵌套偏移和 `unsafe.Add` 直接读写嵌入
字段（string、`*fLeaf`、`fMeta`、int32、[]byte），把 `*fExt` 下转为 `*fBase` 别名视图；
再通过字段指针写入 string/指针字段。最后丢弃 `*fExt` 变量，只保留 `*fBase`、字段指针与
内部 string 字段指针，跨显式 GC 校验扩展尾部（data/tail）仍被完整扫描。

## 期望行为

1. 嵌入 base 偏移为 0，`Sizeof(base) <= Sizeof(ext)`，前缀字段偏移与独立 base 一致；
2. 字段偏移读出的 string/`*fLeaf`/meta/label/[]byte/int32 与类型化字段值一致；
3. string 字段指针的 data 地址与字段本身相同（零拷贝），[]byte 头指向同一底层数组；
4. int32 字段的 `[4]byte` 视图写入后类型化字段可见，恢复后值复原；
5. 通过偏移写入 string 与 `*fLeaf` 字段后，扩展类型立即观察到新值；
6. 丢弃扩展类型变量、只保留 base 视图/字段指针并 GC 后，base 字段、data、extra、tail 全部正确；
7. 只保留 string 字段内部指针并 GC 后，可反推出 `*fExt`，且 data/tail 等扩展尾部字段仍有效；
8. 末行 `ALL PASS l07_field_view_layout_prefix`，失败退出 1。

## 失败判读

- 字段偏移值不符：嵌入布局/Offsetof 计算错误；
- 写入后扩展视图未见变化：字段别名/写屏障缺失；
- GC 后 tail/data 为空或崩溃：GC 按视图静态类型只扫描了 base 前缀，未按分配类型扫描整个 fExt；
- 内部 string 指针 GC 后失效：字段内部指针未被当作整对象根；
- 仅 clobberfree/efence 失败：被替换的旧字段对象被提前回收或清零。

## 参数

- `UAF_SEED`：决定对象 id/字段值/尾部长度，失败信息带种子；
- 无 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_field_view_layout_prefix/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
