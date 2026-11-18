---
id: l17_method_named_defer_chain
dim: L17_stackmap
priority: P0
sources:
  - nats-server@v2.11.0: server/filestore_test.go:3823（fileStore.reportMeta 命名返回 + defer RUnlock）
  - quic-go@v0.50.1: send_stream_test.go:30（writerWithTimeout.Write 方法命名返回，写路径穿插 GC）
tags: [L17, stackmap, method, named-return, defer-chain, panic, recover, LIFO, GC, UAF_SEED]
requires: []
runtime: "<3s（默认 10 轮）"
---

# 场景

指针 receiver 方法的命名返回是含指针/切片/字符串的 struct。方法内依次注册 5 个循环 defer（每个带显式 int 实参）、方法值 `p.mark`、方法实参 `p.stamp(&out, base)` 与一个 recover defer，然后 panic。每个 defer 内都 GC 并读取/改写命名返回值、receiver 与注册时求值的实参；recover 后剩余 defer 继续按 LIFO 执行。caller 收到命名返回 struct 后再 GC 两次并逐字段校验。

## 期望行为

1. panic 被 recover，`out.recovered==true`、`tag=="recovered"`；
2. `out.total == 1(body) + 15(5 个循环 defer) + 100(stamp) == 116`；
3. `out.head`/`head.next`、`out.hook` 与 receiver `p.keep` 在所有 defer 内 GC 后仍正确；
4. defer 执行顺序为 recover → stamp → mark → 5 个 loop，`p.ops` 长度 9 且前 4 项依次为 `body/recover/stamp/mark`；
5. 循环 defer 追加的 `out.list` 按 LIFO 排列为 base+24,base+23,…,base+20；
6. 最后一行 `ALL PASS l17_method_named_defer_chain`，退出码 0。

## 失败判读

- `named return head corrupted`：命名返回槽在 defer/panic 展开期间未被扫描；
- `receiver keep corrupted`：receiver 指针副本或 defer 实参副本漏标；
- `total/list` 不符：defer 记录中的返回值/切片头被破坏，或 defer 顺序异常；
- `stamp hook corrupted`：defer 中新建并写入命名返回的对象未被后续 GC 保留；
- 仅 clobberfree 下失败：defer 记录引用的对象被提前回收；
- 崩溃：panic/deferreturn 路径栈映射错误。

## 参数

- `UAF_SEED`：决定每轮 base，失败输出带种子；
- 无 TORTURE 参数，10 轮固定，P0 预算内。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_method_named_defer_chain/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
