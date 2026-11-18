---
id: l17_closure_mutated_capture
dim: L17_stackmap
priority: P1
sources:
  - fasthttp@v1.59.0: server_test.go:2914（TestTimeoutHandlerTimeout 闭包 handler + ready/done channel）
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37（批量调度闭包按序执行）
tags: [L17, stackmap, closure, closure-table, mutated-capture, interface-boxing, noinline, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

闭包捕获一个可重绑定的 `*item` 变量 `cur` 和一个稳定的 `*item` `keep`。每次调用先把 `cur` 指向新对象（旧链通过 next 保留），再把捕获值复制到局部变量后 GC；随后经 noinline `sinkPair` 再次 GC 并校验。闭包值装箱进 `any`，调用前先 GC 再做类型断言，覆盖闭包对象 interface data word 与调用帧内捕获副本。另有一批独立闭包全部构造完成、统一 GC 后逐个经 `any` 调用。

`UAF_SEED` 决定 step 与每轮 base 偏移。

# 期望行为

1. 每次调用后新的 `bag.head` 链长度等于 `hits+1`（初始捕获节点 + 每次 prepend）；
2. 捕获的 `keep` 链在多次 GC 后保持不变；
3. 经 `any` 断言调用闭包前后，GC 不破坏闭包对象与捕获指针；
4. 批量构造的 6 个闭包各自捕获独立 base，GC 后逐个调用均正确；
5. 退出码 0，最后一行 `ALL PASS l17_closure_mutated_capture`。

# 失败判读

- `reassigned capture corrupted` / `keep capture corrupted`：闭包 context 或调用帧内捕获变量未被扫描；
- `closure type assertion failed`：闭包接口值被破坏；
- `chain length`：捕获链在 GC 后丢失节点；
- `value changed after sink GC`：noinline 调用返回后局部指针副本失效；
- SIGSEGV：闭包 context 中指针被提前回收。

# 参数

- `UAF_SEED`：决定 step、base 与失败时输出；
- 固定 8 轮 × 5 次交替调用 + 6 个批量闭包。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_closure_mutated_capture/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
