---
id: l06_interface_embedding_chain
dim: L06_reflect_iface
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 接口包装链与 io.ReaderFrom 动态分派
  - fiber@v2.52.6: mount_test.go:56 多层嵌套包装后的统一接口派发
tags: [L06, interface-embedding, wrapper-chain, method-set, itab, GC]
requires: []
runtime: "<10s"
---

# 场景

定义 `reader`/`writer`/`closer` 递进嵌入成 `readWriteCloser`，再由 `mega` 叠加 `labeler`；`device` 通过匿名嵌入提升方法，`wrap` 组成 5 层包装链。每层调用都跨 GC，验证接口嵌入后的动态派发、接口到接口断言、值/指针接收者方法集与全局接口槽保活。

# 期望行为

1. 嵌入接口在 GC 后可读、可写、可关闭，值分别为 `buf+7`、`buf+7`、`id*1000+buf+7`；
2. 5 层 wrapper 链的 Read 在基础上加 5，Close 透传到 device 并置 `closed=true`；
3. 值接收者 `vadd` 的值与指针都满足 `reader`，结果为基础值 +3；
4. 对未实现接口的断言返回 `ok=false`；接口到接口断言成功；
5. 最后一行 `ALL PASS l06_interface_embedding_chain`。

# 失败判读

- `embedded interface read after GC` / `wrapper chain read`：包装链中的 `inner` 接口或 `tag` 指针跨 GC 失效，检查接口数据区栈映射；
- `close propagated`：wrapper 转发丢失；
- `assert unimplemented interface fails`：itab 缓存/类型断言错误；
- 崩溃：匿名嵌入方法的 receiver 指针未正确保活。

# 参数

无随机路径；runner 仍注入 `UAF_SEED` 并仅在失败输出中打印。

# 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_interface_embedding_chain/*.go
GODEBUG=clobberfree=1 /tmp/case
```
