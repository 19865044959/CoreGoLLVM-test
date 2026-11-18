---
id: l06_iface_embed_diamond
dim: L06_reflect_iface
priority: P1
sources:
  - caddy@v2.10.0: modules/caddyhttp/responsewriter_test.go:47 ResponseWriterWrapper 按 io.ReaderFrom 断言分派
  - gin@v1.10.0: context_test.go:2686 嵌入 ResponseWriter 接口的 wrapper 替换中间件 writer
  - fiber@v2.52.6: mount_test.go:56 三层嵌套包装后统一接口派发
  - gin@v1.10.0: response_writer_test.go:107 Hijacker/CloseNotifier 接口断言
tags: [L06, interface-embedding, diamond, method-set, Implements, AssignableTo, stdlib-io, nil-embedded-interface, GC]
requires: [reflect]
runtime: "<10s"
---

# 场景

HTTP 中间件通过嵌入 `http.ResponseWriter` 的 wrapper 转发调用，并在运行时按 `io.ReaderFrom`、
`Hijacker` 等能力接口断言分派。本用例用 stdlib `io` 接口构造菱形嵌入
（`ReadCloser` 与 `ReadWriter` 都嵌入 `io.Reader`，再合并为 `ReadWriteCloser`），校验方法集
去重、接口间 `Implements`/`AssignableTo` 方向、命名/匿名接口身份，以及 nil 嵌入接口与包装链
跨 GC 的动态派发。

## 期望行为

1. 退出码 0，最后一行 `ALL PASS l06_iface_embed_diamond`；
2. 菱形嵌入的 `SeekReadWriteCloser` 方法数为 4（`Close/Read/Seek/Write`），`Read` 只出现一次，
   `Method(i).Func` 对接口类型恒为零值；
3. 指针 `*buffer` 实现组合接口，值 `buffer` 因指针接收者不实现；接口类型间
   `Implements`/`AssignableTo` 方向正确（宽接口可赋给窄接口，反向不可）；
4. 命名接口与结构相同的匿名接口类型不相等但可双向赋值；相同匿名接口类型相等；
5. 嵌入接口的 wrapper 值获得被嵌入接口的方法集，但不额外获得 Seeker；通过 wrapper 链读取
   在 GC 后仍返回完整数据；
6. nil 嵌入接口字段上的提升方法调用 panic；
7. 动态类型断言（ReaderAt/Seeker）与接口值同一性在 GC 后保持不变。

## 失败判读

- `has N methods`：菱形嵌入方法集未去重或提升规则错误；
- `implements ...` / `assignable ...`：接口方法集/赋值方向判定错误；
- `named and unnamed interface ...`：接口类型身份被错误合并或拆分；
- `read through wrapper chain after GC`：wrapper 内接口字段未扫描；
- `... did not panic`：nil 嵌入接口调用未触发 panic。

## 参数

- `UAF_SEED`：wrapper 与 buffer 负载值来源；失败输出带出种子。

## 运行

```bash
go build -o /tmp/case ./cases/L06_reflect_iface/l06_iface_embed_diamond/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
