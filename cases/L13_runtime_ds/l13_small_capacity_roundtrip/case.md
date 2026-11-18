---
id: l13_small_capacity_roundtrip
dim: L13_runtime_ds
priority: P2
sources:
  - klauspost/compress@v1.17.11: s2/s2_test.go:161,174 小目标容量 nil/20/64 × 多种输入长度的编码/解码扩容路径
tags: [L13, flate, small-capacity, append-growth, roundtrip, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

模拟 klauspost/compress s2 的 `Encode(dst, src)` 小目标容量测试：目标缓冲容量为 nil / 20B / 64B，输入长度取 32 档（0、1、2、3、4、5、7、8、15、16、17、31、32、33、63、64、65、127、128、129、255、256、257、511、512、513、1023、1024、1025、2048、4095、8192）。用 stdlib `compress/flate` 通过 append 型 writer 输出，强制走小容量起步的扩容路径。每轮先编码全部组合、再 GC、最后统一解码，验证输出缓冲与输入切片的所有权独立。输入含三分之一的强可压缩数据，其余为固定种子伪随机数据。

# 期望行为

1. 96 个“长度 × 目标容量”组合全部编码成功，输出非空且 `cap >= len`；
2. 至少一个组合的编码输出超过 64B（证明小容量目标确实发生扩容）；
3. 编码后把源切片全部改写为 `0x5A`，GC 后解码结果仍与原始输入逐字节一致（输出不引用源切片）；
4. 解码同样使用 20B/64B 小目标容量，结果正确；
5. 相同输入、相同目标容量重复编码两次，输出逐字节一致（确定性）；
6. 每轮编码后与解码后各做一次显式 GC + 垃圾分配，缓冲跨 GC 存活；
7. 最后一行 `ALL PASS l13_small_capacity_roundtrip`。

# 失败判读

- 解码内容与原始输入不一致：flate 往返或源切片所有权断言失败；
- 输出缓冲在下一次写入/GC 后被覆盖：append 型 writer 复用了不应复用的底层数组；
- 两次编码结果不同：编码路径确定性被破坏（编译器/运行时问题，而非本用例随机性——随机数据由固定种子生成）；
- 小容量组合 panic/越界：append 扩容路径或边界检查问题；
- 超时：TORTURE_ROUNDS 过大或 GC 饥饿。

# 参数

- `UAF_SEED`：伪随机输入数据的种子，失败输出带该种子；
- `TORTURE_ROUNDS`：重复轮数，默认 3，范围 1..1000；放大后默认规模仍能快速通过。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_small_capacity_roundtrip/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=20 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
