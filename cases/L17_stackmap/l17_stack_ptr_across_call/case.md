---
id: l17_stack_ptr_across_call
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A1 栈上指针未标记（跨调用）
tags: [L17, stackmap, noinline, chan, interface]
requires: []
runtime: "<2s"
---

# 场景

模拟服务型代码中最常见的形态：请求处理函数里同时持有结构体指针、字符串、指针切片、map、channel 和 interface 值，然后调用一个 noinline 的辅助函数（内部触发 GC），返回后继续使用全部对象。

# 期望行为

1. `midCall` 的求和等于 186（1+2+11+10+20+32+11+44+55），说明 GC 期间所有指针参数都被正确扫描；
2. 返回后再次 `runtime.GC()`，`a.next.v == 2`、字符串内容、slice/map/interface 内容全部不变；
3. 50 轮循环稳定，退出码 0，最后一行 `ALL PASS l17_stack_ptr_across_call`。

# 失败判读

- 求和不为 186 / `FAIL: ...`：某个指针在调用点未被栈映射标记，GC 回收了仍存活对象；
- SIGSEGV / `fatal error: found bad pointer`：漏标或写屏障缺失导致对象被释放；
- 只在 `efence` 下崩溃：UAF 已发生但默认堆未复用。

# 参数

无随机路径；`UAF_SEED` 不影响结果。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_stack_ptr_across_call/*.go
GODEBUG=clobberfree=1 /tmp/case
```
