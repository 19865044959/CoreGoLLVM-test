# Go 编译器/运行时 UAF 历史著名案例与覆盖缺口

> 2026-11-28。来源：golang/go issue tracker、go.googlesource.com 提交、gccgo/libgo 补丁、nanogo 修复记录（链接见文末）。
> 对照：本仓 58 个 stress 场景（s/n/u/x/g/y 系列）的覆盖情况。

## 1. 历史案例表

| # | 问题 | 年份 | 机制（一句话） | 失败签名 | 本仓覆盖 |
|---|---|---|---|---|---|
| 1 | [go#10353](https://go.googlesource.com/go/+/878a86a129ceb771f677c8391a9f5c891e5be7c3) | 2015 | 逃逸分析错误：闭包捕获局部变量后闭包逃逸到堆，堆持有**栈**指针 → 栈帧回收后 UAF | 依赖后续使用形态 | ❌ **完全未覆盖**（z08） |
| 2 | [go#16249](https://tip.golang.org/test/fixedbugs/issue16249.go) | 2015 | defer 函数中**堆提升的结果参数** liveness 计算错误 | `bad pointer in frame main.A ... 0x1` | 部分（g01/y02 是"槽未清零"，不是"堆提升结果参数 liveness"） |
| 3 | [go#22458](https://go.googlesource.com/go/+/6fc1242ea8f8bbacd88cb834ef61dbe9e84a8514) | 2017 | KeepAlive 必须引入对 spill 槽的 use，否则 spill 不支配 KeepAlive → 对象提前回收 | 提前终结/悬空 | ❌ 未覆盖（z01） |
| 4 | [go#30476](https://tip.golang.org/test/fixedbugs/issue30476.go) | 2019 | **KeepAlive 作用于"可重物化地址"的栈对象时被忽略** → 仅由该栈对象引用的堆对象被提前回收 | finalizer 提前执行 | ❌ 未覆盖（z01） |
| 5 | [go#37688](https://github.com/golang/go/commit/825ae71e567593d3a28b7dddede8745701273c52) | 2020 | open-coded defer panic 路径：newdefer 把 defer 记录挂到 **g0** 链表 → 陈旧记录保活已回收的 defer 结构；freedefer 不清零 → GC 扫到坏指针 | `bad pointer` / GC 崩溃 | ❌ 未覆盖（z05） |
| 6 | [go#37782/#37664](https://go.googlesource.com/go/+/1d6a510081e281c2a580f728a53c236c79ea9258) | 2020 | 递归 panic/recover 后 **陈旧的 open-coded defer 条目**残留链表 → 执行时 SEGV | SEGV | ❌ 未覆盖（z05） |
| 7 | [go#41816](https://github.com/golang/go/issues/41816) | 2020 | **init 函数里 mapassign 触发栈复制**，init 帧指针映射错误 | `invalid pointer found on stack`（Go 1.15 回归） | ❌ 未覆盖（z09） |
| 8 | [go#44623](https://github.com/golang/go/issues/44623) | 2021 | open-coded defer 的闭包槽总被视为 live（泄漏；反向省略即 UAF） | 泄漏/finalizer 延迟 | 部分（u08/x05 不测 defer 记录槽） |
| 9 | [go#57170](https://github.com/golang/go/issues/57170) | 2022 | os/user + cgo 分配变更导致 cgo 帧坏指针 | `bad pointer in frame os/user.lookupUnixGid` | 否（库级，非本仓范畴） |
| 10 | [go#75477](https://github.com/golang/go/issues/75477) | 2022 | mips64 **zerorange 漏了 +8 偏移** → 清零区间错位，指针槽盖不全 | `bad pointer in frame main.invoke ... 0x7` | ❌ 未覆盖（z07；x05/y01 打"未初始化"，不打"清零区间"本身） |
| 11 | [go#82865 / #83420](https://github.com/golang/go/issues/83420) | **2026（Go 1.27 新回归）** | **循环不变量提升（LICM）把 unsafe.Pointer 算术移出守卫**，无效指针在 safepoint 上"存活" | `invalid pointer found on stack` | ❌ 未覆盖（z04） |
| 12 | [go#75778](https://github.com/golang/go/issues/75778) | 2022+ | **reflect.MakeFunc 的 register-ABI RegArgs 假栈对象**：指针只在寄存器 → 扫描缺失 | `marked free object in span` / ASan use-after-poison | ❌ 完全未覆盖（z03） |
| 13 | [gccgo WB 补丁](https://cgit.ipfire.org/thirdparty/gcc.git/log/libgo?h=trunk&id=09e18d113b3c3dae896ac1a8ad1e0087adbb153b) | 2019 | gccgo 前端插写屏障，无法阻止**对未初始化栈内存**的写 → 屏障缓冲区含无效指针（扫描侧需容错） | 坏指针/误标 | 部分（n04/u01 只写已初始化字段；y07 栈 dst 有覆盖） |
| 14 | [gccgo gcstack 补丁](https://patchwork.ozlabs.org/project/gcc/patch/CAOyqgcU-CDQh+LPJPEZVQMhaHE1Q8kT+ahAoP2+Er5EAzOXPLg@mail.gmail.com/) | 2019 | **新建 C 线程回调创建的 extra G 保留了旧 gcstack**（指向已 unmapped 的栈段）→ GC 扫描已释放内存 | GC 崩溃 | ❌ 未覆盖（z06；x07 线程持续存在） |
| 15 | [go#34474 / #34684](https://github.com/golang/go/issues/34684) | 2019 | `//go:uintptrescapes` 对**方法调用/方法表达式/函数值/接口**形态不一定生效 → 对象在调用中可能被回收 | 调用期 UAF | ❌ 完全未覆盖（z02） |
| 16 | [go#79592](https://github.com/golang/go/issues/79592) | 2024 | 泛型实现缺逃逸分析（性能问题；反向的"过度逃逸忽略"即 UAF） | 分配放大 | 部分（y03 泛型返回） |
| 17 | [go#68220 / #68047](https://github.com/golang/go/issues/68047) | 2023 | unsafe 误用类 `found pointer to free object` | 已由用户契约排除 | n/a |
| 18 | [go#80799](https://git.codeproxy.net/golang/go/issues/80799) | 2026 | Green Tea GC 下 reportZombies 诊断本身错误（运行时报告 bug，非代码生成） | 诊断误导 | n/a |
| 19 | [#44900/#45364/#47415/#54247](https://github.com/golang/go/issues/44900) | 2020-22 | Windows 并发 exec + 分配压力的 GC 竞态族 | 偶发 GC 崩溃 | 部分（n06 exec churn 无分配压力叠加） |

## 2. 覆盖缺口（按"完全未覆盖"排序）

| 缺口 | 对应历史案例 | 本仓现状 |
|---|---|---|
| **liveness / KeepAlive 类**（对象被提前回收，而非栈槽脏） | #3 #4 #2 | 所有场景把 KeepAlive 当工具，从未把它当**被测目标**；无 finalizer 提前触发探针 |
| **编译器指令遵守**（`//go:uintptrescapes` 各调用形态） | #15 | grep 全仓 0 处 |
| **register-ABI + reflect 调用路径**（RegArgs 假栈对象） | #12 | 0 处 `reflect.MakeFunc`；`Value.Call` 仅 u06 顺带用 |
| **守卫内指针算术被优化提升**（LICM/边界守卫） | #11（1.27 新回归） | 0 处专门形态 |
| **open-coded defer 记录生命周期**（panic 路径 g0 链表 / recover 后陈旧条目） | #5 #6 #8 | u08/x05 测展开帧内容，不测 defer 记录本身 |
| **zerorange 清零区间**（混合类型大帧、多分支条件初始化） | #10 | x05/y01 依赖"未清零"制造脏栈，但从不校验**该清零的区间确实清零且不多不少** |
| **逃逸分析失败**（堆持有栈指针） | #1 #16 | 0 处针对闭包/append/接口/方法值逃逸的定向形态 |
| **新 C 线程 + 栈增长**（extra G/gcstack） | #14 | x07 线程常驻，不覆盖"每轮新建线程" |
| **init 期栈增长** | #7 | u05 有 init 但无 mapassign+morestack 组合 |

## 3. 设计结论

历史 UAF 分两大类：
- **A 类：栈/寄存器侧描述错误**（bad pointer on stack）——已由 x/y/g 系列大量覆盖；
- **B 类：对象生命周期错误**（坏指针走了堆/被提前回收/记录残留）——**覆盖率低**，尤其 KeepAlive、指令遵守、reflect ABI、defer 记录、逃逸分析五个方向。

第五批（z01–z09）全部对准 B 类与 A 类中未覆盖的 zerorange/LICM 机制，设计见 `stress_batch5_design.md`。

## 4. 来源

- [go#83420 LICM unsafe.Pointer](https://github.com/golang/go/issues/83420) / [go#82865](https://github.com/golang/go/issues/83420)
- [go#75477 mips64 zerorange](https://github.com/golang/go/issues/75477)
- [go#41816 init mapassign stack copy](https://github.com/golang/go/issues/41816)
- [go#57170 os/user bad pointer](https://github.com/golang/go/issues/57170)
- [go#44623 open-coded defer pointer args](https://github.com/golang/go/issues/44623)
- [go#37688 defer record on g0](https://github.com/golang/go/commit/825ae71e567593d3a28b7dddede8745701273c52)
- [go#37782 stale open-coded defers](https://go.googlesource.com/go/+/1d6a510081e281c2a580f728a53c236c79ea9258)
- [go#75778 reflect MakeFunc RegArgs](https://github.com/golang/go/issues/75778)
- [go#30476 KeepAlive rematerializable](https://tip.golang.org/test/fixedbugs/issue30476.go)
- [go#22458 KeepAlive spill](https://go.googlesource.com/go/+/6fc1242ea8f8bbacd88cb834ef61dbe9e84a8514)
- [go#16249 defer heap-promoted result](https://tip.golang.org/test/fixedbugs/issue16249.go)
- [go#10353 closure escape](https://go.googlesource.com/go/+/878a86a129ceb771f677c8391a9f5c891e5be7c3)
- [go#34684 uintptrescapes call forms](https://github.com/golang/go/issues/34684)
- [go#79592 generics escape](https://github.com/golang/go/issues/79592)
- [gccgo gcstack for extra G](https://patchwork.ozlabs.org/project/gcc/patch/CAOyqgcU-CDQh+LPJPEZVQMhaHE1Q8kT+ahAoP2+Er5EAzOXPLg@mail.gmail.com/)
- [gccgo write-barrier buffer conservative scan](https://cgit.ipfire.org/thirdparty/gcc.git/log/libgo?h=trunk&id=09e18d113b3c3dae896ac1a8ad1e0087adbb153b)
- [nanogo: zero frame object pointer words](https://github.com/golang-design/nanogo/commit/a25b2b604447f59ac8fe656887ae10e5a1a3a24a)
