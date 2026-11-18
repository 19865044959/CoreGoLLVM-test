---
id: l17_variadic_iface_ptr
dim: L17_stackmap
priority: P1
sources:
  - sonic@v1.13.2: ast/api_native_test.go:132（TestStackAny 把栈上对象装箱成 any，GC/FreeOSMemory 后再序列化）
  - nats-server@v2.11.0: server/client_test.go:1634（captureWarnLogger.Warnf(format string, v ...any) 变参接口日志路径）
tags: [L17, stackmap, variadic, interface, []any, method, GC, UAF_SEED]
requires: []
runtime: "<10s（默认 8 轮）"
---

# 场景

模拟日志/编解码注册等 `...any` 接口：调用方把 `*item`、含指针的 `*group`、`[]*item` 与字符串装进 `[]any`，用 `args...` 转发给 noinline callee；callee 在变参切片仍存活时连续两次 GC 并两遍求和。另以指针 receiver 方法 `add(prefix, vals ...any)` 重复同样路径。覆盖变参切片头、接口字（itab+data）与 caller 栈上 `[]any` 的栈映射。

# 期望行为

1. `consume(args...)` 两遍 checksum 相等且等于构造时算出的期望和；
2. callee 返回后 caller 再次 GC，`nodes`/`group` 的全部指针字段与字符串内容不变；
3. 方法 `col.add` 在 GC 后返回相同语义的期望和，receiver 状态 `col.base` 不变；
4. 最后一行 `ALL PASS l17_variadic_iface_ptr`，退出码 0。

# 失败判读

- `FAIL: fold arg[i]` / checksum 不等于期望：变参切片元素或接口 data 未按指针扫描，对象已被回收；
- 仅在 clobberfree/efence 下失败：UAF 已发生，默认堆未复用；
- SIGSEGV / `fatal error: found bad pointer`：接口字中的 data 指针被破坏；
- 超时：不应发生（无阻塞路径）。

# 参数

- `UAF_SEED`：决定每轮指针参数个数（8..14）与参数排列顺序，同时写入失败信息；不影响期望结果的正确性。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_variadic_iface_ptr/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
