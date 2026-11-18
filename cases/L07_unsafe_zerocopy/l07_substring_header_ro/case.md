---
id: l07_substring_header_ro
dim: L07_unsafe_zerocopy
priority: P1
sources:
  - echo@v4.13.4: middleware/logger_test.go:175 *json.RawMessage 经 unsafe.Pointer 重解释为 string 头
  - sonic@v1.13.2: internal/caching/hashing_test.go:50 unsafe 读 string 头逐字节计算 FNV-1a
  - goccy-json@v0.10.5: path_test.go:12 路径抽取返回输入子切片
tags: [L07, unsafe, string-header, substring, read-only, liveness]
requires: []
runtime: "<10s"
---

# 场景

只读子串 header：从父串 `{data,len}` 头按 `unsafe.Add(ph.data, off)` 派生 child/grandchild/tail header，手工重解释为 string；覆盖重叠子串、多字节 UTF-8 子串、尾部零长 header，以及与 `unsafe.String` 直接构造的地址一致性。父串变量丢弃后仅由子串头与全局 header 保活，跨显式 GC 校验内容与 FNV 哈希；不写入任何 string 数据。

## 期望行为

1. 父串 header 的 data/len 与 `unsafe.StringData`/len 一致；
2. 子串 header 的 data 等于父串 data + 偏移，手工 header 与 `unsafe.String` 指向同一地址、内容相等；
3. 重叠子串共享字节，tail 前 5 字节等于 child 后缀；UTF-8 子串长度按字节计，零长尾部 header 长度为 0；
4. 父串变量置空并 GC 后，所有保留子串内容与哈希不变，全局 `{data,len}` header 可重建同一子串；
5. 多轮解析、每轮丢弃父串并 GC，保留视图仍各自正确；
6. 反复只读访问后哈希稳定，证明未修改源数据；
7. 末行 `ALL PASS l07_substring_header_ro`。

## 失败判读

- header data/len 不符：string 头布局或偏移计算错误；
- GC 后内容错乱/SIGSEGV：子串 header 的 data 指针未被当作 GC 根，父串底层数组被回收；
- UTF-8 长度错误：把字节长度当成 rune 数量；
- 只读路径哈希变化：用例误写 string 数据（内存破坏）；
- 仅 stress/k8s 失败：GOGC=1 下 header 未被栈/全局位图标记。

## 参数

- `UAF_SEED`：决定父串编号、子串起点与内容，失败信息带种子；
- `TORTURE_ROUNDS`：轮数，默认 24；每轮显式 GC，放大可做长压测。

## 运行

```bash
go build -o /tmp/case ./cases/L07_unsafe_zerocopy/l07_substring_header_ro/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=2000 UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
