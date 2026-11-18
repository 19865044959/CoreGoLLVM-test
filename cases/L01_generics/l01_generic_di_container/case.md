---
id: l01_generic_di_container
dim: L01_generics
priority: P1
sources:
  - caddy@v2.10.0: modules_test.go:22 TestGetModules 按模块 ID 前缀从全局注册表解析并返回子模块集合
  - kitex@v0.13.0: client/option_test.go:356 TestProxyWithConnPool 通过 client option 向调用链注入 connpool 依赖
tags: [L01, generics, dependency-injection, container, singleton, transient, cycle-detection, type-assertion, gc, UAF_SEED]
requires: [generics]
runtime: "<10s"
---

# 场景

自建泛型依赖注入容器：`registerDI[T]` 把 `diProvider[T] = func(*Container) (T,error)` 以名称存入 `any` 表，`resolveDI[T]` 做类型断言、循环检测、单例缓存/瞬态重复构造。对应 caddy 模块注册表的“按 ID 解析服务”与 kitex client option 的组件注入。

注册依赖图 config←logger←store←app（app 为瞬态），注册顺序由 UAF_SEED 打乱，解析 app 时必须按依赖拓扑执行且 trace 顺序确定；第二次解析 app 只执行 app provider，并共享 config/store/logger 单例。固定用例覆盖单例指针稳定、瞬态指针不同、切片值类型单例、缺失服务、类型不匹配、重复注册、容器隔离、直接/间接循环（单例与瞬态）以及循环错误后的容器可用性；显式 GC 后核对单例指针与 blob 内容。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_di_container`；
2. 打乱注册顺序后首次 Resolve(app) 的 trace 恰为 `app,store,config,logger`，各 provider 只执行一次；
3. 第二次 Resolve(app) 返回不同指针，但 store/config/logger 与第一次相同，trace 仅追加 `app`；
4. 单例 config 多次解析指针相同且不重复执行 provider；`[]string` 值类型单例解析正确；
5. 缺失服务、Resolve 类型不匹配（int vs *diConfig）、重复注册均返回错误，重复注册不覆盖原实现；
6. 两个容器不共享单例实例；
7. 单例与瞬态的依赖环均被检测并返回错误，`resolving` 集合清空；出错后容器仍可注册并解析新服务；
8. 显式 GC 后单例指针身份与 config blob 不变，瞬态 app 仍持有同一批单例。

# 失败判读

- `init order` / `init trace`：provider 求值顺序错误或单例未缓存导致重复初始化；
- `singleton identity mismatch`：单例被按值拷贝或缓存 key 错误；
- `transient app returned same pointer`：生命周期标志丢失；
- `cycle was not detected` / `resolving set not cleaned`：循环检测缺少 defer 清理，可能死锁或污染后续解析；
- `type mismatch was not reported`：泛型 provider 断言失败被吞掉；
- `SIGSEGV` / `fatal error: found bad pointer`：`any` 中保存的指针/切片在 GC 中被破坏（偏 L06/L18）。

# 参数

- `UAF_SEED`：决定注册顺序的打乱方式；失败输出带种子；
- 无 TORTURE 参数（P1 固定依赖图，单次 <10ms）。

# 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_di_container/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
