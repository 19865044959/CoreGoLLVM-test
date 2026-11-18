---
id: l17_named_ret_method_sink
dim: L17_stackmap
priority: P1
sources:
  - nats-server@v2.11.0: server/filestore_test.go:3823（reportMeta 命名返回 + defer RUnlock）
  - grpc-go@v1.71.1: test/end2end_test.go:4369（lockingWriter.Write 命名返回 n/err + defer Unlock）
tags: [L17, stackmap, method, named-return, defer, deferreturn, method-value, escaped-result, GC, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

`proc.run` 有三个命名返回值（struct、`*item`、error）。它注册两个 defer：一个把命名返回槽的地址交给 `note` 方法值并登记到 `proc.notes`，另一个闭包在 deferreturn 前改写命名 struct（追加切片元素、改 text、重设 head）。方法内、note 内与闭包内都插入 GC。由于 `&out` 逃逸，defer 运行结束后 caller 仍能通过 `notes` 指针读到命名返回槽的最终状态；同一方法再通过方法值调用一次，覆盖 `mv := p.run` 路径。

`UAF_SEED` 决定每轮 base 与 sel（影响返回值指针/切片内容）。

# 期望行为

1. defer 执行顺序为 `note` → `closure`（LIFO），每轮两条记录；
2. note 在登记时看到 `text=="body"`、`len(list)==2`，且 `out.head` 与 id 一致；
3. closure defer 结束时命名返回变为 `text=="body+defer"`、`list` 长度 3、head 已重设；
4. caller 收到的 struct/`*item`/error 与 notes 中逃逸指针内容一致，并在 GC 后仍有效；
5. 直接调用与方法值调用各 10 轮，共 20 条笔记，顺序与计数全部匹配；
6. 退出码 0，最后一行 `ALL PASS l17_named_ret_method_sink`。

# 失败判读

- `named return ... corrupted`：命名返回槽或 `&out` 指针未被栈图覆盖；
- `notes[...]`：逃逸后的命名返回对象被提前回收或内容错乱；
- `defer order`：defer 记录顺序异常（非 GC 问题时也可能出现）；
- `list backing changed`：命名返回中的切片在复制返回时丢失；
- SIGSEGV：逃逸指针失效。

# 参数

- `UAF_SEED`：每轮 base/sel 种子，失败输出带该值；
- 固定 10 轮 × 2 种调用形态。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L17_stackmap/l17_named_ret_method_sink/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
