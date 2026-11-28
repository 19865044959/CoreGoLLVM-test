# 第五批（z01–z09）设计：生命周期类 UAF 与未覆盖的编译机制

> 依据 `stress_history_bugs.md`（历史案例表）与 §2 覆盖缺口。不与现有 58 场景重复。

| ID | 场景 | 对准的机制 | 历史案例 | 优先级 |
|---|---|---|---|---|
| z01 | keepalive_liveness | **KeepAlive 作为被测目标**：可重物化栈地址 + 仅由栈对象引用的堆对象；finalizer 提前触发 = UAF | #3 #4 #2 | P0 |
| z02 | uintptr_directive_forms | **`//go:uintptrescapes` 各调用形态**：直接/方法值/方法表达式/函数值/接口；指令被忽略 → 调用中对象被回收 | #15 | P0 |
| z03 | reflect_makefunc_regabi | **reflect.MakeFunc + register ABI RegArgs**：指针只在寄存器、假栈对象、GC 在回调内 | #12 | P0 |
| z04 | licm_guarded_unsafe | **守卫内 unsafe.Pointer 算术**被 LICM 提升：边界/nil/类型守卫 × 循环内 GC | #11 | P0 |
| z05 | opendefer_record_lifecycle | **open-coded defer 记录**：递归 panic/recover 风暴、defer 参数指针、panic 路径记录链表 | #5 #6 #8 | P0 |
| z06 | cgo_newthread_stackgrowth | **每轮新建 C 线程回调 + 回调内深递归栈增长**（extra G/gcstack） | #14 | P1（cgo） |
| z07 | zerorange_mixed_frame | **zerorange 清零区间**：混合类型大帧、多分支条件初始化、帧中途 morestack；校验"该清零的确实清零、该保留的完好" | #10 | P0 |
| z08 | escape_heap_to_stack | **逃逸分析失败面**：闭包捕获逃逸、append 保留栈底层数组、接口/方法值逃逸、全局存局部地址；finalizer+weak 探针 | #1 #16 | P0 |
| z09 | init_time_stackgrowth | **init 内 mapassign + 大帧 + 栈增长**，init 结束后 GC 校验 | #7 | P1 |

## 探针升级（本批核心）

1. **finalizer 提前触发 = UAF**（z01/z02/z03/z08）：对象仍被合法引用时 finalizer 执行 → 立即 FAIL。这是"提前回收"类 UAF 的直接判据，现有场景只做"可达不终结"的弱断言。
2. **weak.Make 强引用断言**（沿用 x14）。
3. **defer 执行计数与参数校验**（z05）：每个开放编码 defer 必须恰好执行一次、参数指针内容正确。
4. **zerorange 三态校验**（z07）：未初始化局部 == 零值（Go 语义保证）、已初始化局部 == 期望值、指针槽非垃圾。
5. **RegArgs 校验**（z03）：回调内 GC 后逐个校验参数指针。
6. **指令形态矩阵**（z02）：5 种调用形态 × 3 种对象大小，任一形态失败即定位到"指令未生效的形态"。

## 验收

- 全部场景在官方 Go 1.24.1 上全 profile 通过（这些是合法程序）；
- 固定轮数 + 3s soak/window + `-race`；
- z06 需 `CGO_ENABLED=1`，`requires: [cgo]`；
- z03 标 `requires: [reflect]`。
