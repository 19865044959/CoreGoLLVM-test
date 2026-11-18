---
id: l11_slice_copy_selfappend
dim: L11_compiler_opts
priority: P1
sources:
  - klauspost-compress@v1.17.11: gzip/gzip_test.go:517 benchmarkGzipN 连续 5 次 dat = append(dat, dat...)
  - klauspost-compress@v1.17.11: compressible_test.go:112 测试数据自追加放大
  - klauspost-compress@v1.17.11: flate/deflate_test.go:88 TestBulkHash4 中 y = append(y, y...)
tags: [slice-copy, overlapping-copy, self-append, memmove, append-growth, shallow-copy, write-barrier, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟压缩库测试数据自倍增与解码窗口拷贝：`append(s, s...)` 在容量内与需扩容两条路径、连续 5 次自倍增、dst/src 区域部分重叠、对子切片追加、byte 切片自追加，以及含指针字段的结构体切片自追加；另验证自追加后浅拷贝共享指针、显式 GC 后指针仍有效。

# 期望行为

1. 容量恰好等于长度（需扩容）与容量翻倍（原地写）两种自追加都得到 2 倍长度，内容等于“源快照+源快照”；
2. 自追加不改变源切片可见前缀；
3. 从 `[3,-7,11]` 连续自追加 5 次得到 96 个元素，且按原 3 元素周期重复；
4. 当 dst 与 src 在同一底层数组上部分重叠时，append 结果等于先快照 src 再追加的语义，且 dst 前缀不变；
5. 对 `base[:2]` 追加 `base[3:]` 的结果与快照模型一致，原前缀不变；
6. byte 切片（含 NUL/0xff/0x80）自追加后逐字节正确；
7. 结构体切片自追加后第 i 与第 i+10 个元素共享指针字段，指针目标修改互相可见；
8. 显式 GC 后共享指针身份和值不变，保留的自追加结果逐元素匹配快照；
9. 默认构建与 `-gcflags='all=-N -l'` 构建全部断言一致；
10. 最后一行输出 `ALL PASS l11_slice_copy_selfappend`。

# 失败判读

- `self append in place content` / `self append grow content`：append 重叠拷贝没有按 memmove 语义处理源快照；
- `overlapping append snapshot semantics`：dst/src 部分重叠时读到已被覆盖的源元素；
- `sub-slice overlapping append`：对子切片追加的覆盖范围/长度错误；
- `byte self append content`：byte 路径的长度或字节被截断；
- `struct self append shares pointer`：结构体浅拷贝被错误深拷贝；
- `struct self append pointers after GC`：写屏障缺失导致指针字段失效；
- 失败输出带 `UAF_SEED`。

# 参数

- `UAF_SEED`：决定每轮切片长度 48..95 与数据 salt，失败输出带种子；
- 无 `TORTURE_ROUNDS`（P1，固定 3 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L11_compiler_opts/l11_slice_copy_selfappend/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
go build -gcflags='all=-N -l' -o /tmp/case_n ./cases/L11_compiler_opts/l11_slice_copy_selfappend/*.go
UAF_SEED=1 /tmp/case_n
```
