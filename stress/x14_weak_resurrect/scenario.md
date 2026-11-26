---
id: x14_weak_resurrect
priority: P1
covers: [L18]
requires: []
default_seconds: 60
max_workers: 16
---

# 场景

weak / finalizer / 复活 / AddCleanup 全套（P1）。n03 只覆盖“可达不终结、失活最终终结”；x14 补齐终结器复活、重新武装、AddCleanup 多注册/Stop/交错，以及 `weak.Pointer` 的“活对象被回收”直接判据。

每 worker 每轮：

1. **anchor 探针**：一个强引用常驻对象注册 finalizer，每轮 GC 后断言 `anchor_finalized==0`（可达被终结 = FAIL）；
2. **plain weak**：对象 + `weak.Make`，在 GC 期间保持强引用，断言 `Value()!=nil`；失活后轮询（15s 上限）直到 `Value()==nil`；
3. **interior weak**：`weak.Make(&h.pad[1])`，强持有 `h` 时 `Value()!=nil` 且值正确；失活后轮询转 nil；
4. **tiny / zero-size**：tiny 用 weak 做“强持有非 nil”判定；zero-size 按 Go 文档不保证 weak/cleanup 语义（`weak.Make` 对 zero-size 会 throw），只注册 finalizer+cleanup 并断言“强持有期间不得提前触发”，最终是否执行只记录；
5. **终结器复活**：`fin1` 在终结器内把对象存回强引用槽（复活）并 `runtime.SetFinalizer(p, fin2)` 重新武装；随后清槽、GC、等待 `fin2` 再次终结；再次清槽后不再注册，断言没有第三次终结；
6. **AddCleanup 同对象多注册 + Stop**：3 个 cleanup，ord2 立即 `Stop()`；断言 ord1/ord3 各执行一次、ord2 永不执行、执行参数与注册值一致；强持有期间零执行；
7. **AddCleanup 跨对象交错**：A/B 交错注册、各自 Stop 一项；只丢 A 时 B 的 cleanup 不得执行（B 仍强持有），再丢 B 后各自恰好执行保留项；任何额外/重复事件 FAIL；
8. **interior cleanup**：cleanup 注册在 `&h.pad[1]` 上，强持有期间零执行，整个对象失活后执行一次。

cleanup 执行顺序按 Go 文档**未定义**，因此断言的是“精确集合 + 每项恰好一次 + Stop 生效 + 不提前”，而不是次序。

**finale profile**：直接支持，不使用 `.skip-envs`。支持 `checkfinalizers` 的 Go 版本下，可达对象被终结/cleanup 泄漏会由运行时直接 throw，与本场景自身断言互补；Go 1.24.1 不认识该 GODEBUG 键（静默忽略），场景检查仍全部生效。`soak/finale` 下 `clobberfree=1` 会让复活对象的内容破坏当场现形。

## 不变量

1. 可达 anchor 的 finalizer 永不执行（每轮与收尾各查一次）；
2. 持强引用时 weak `Value()!=nil`；plain/interior 对象失活后 `Value()` 在轮询上限内转 nil；
3. 复活对象两次终结各恰好一次，canary 在终结器内、复活槽内、再次失活前均正确；不注册时无第三次终结；
4. AddCleanup：注册集合中保留项各执行一次，`Stop()` 项零执行，强持有期间零执行，参数与注册值一致；
5. interior cleanup 在 holder 仅由内部指针保活/失活后正确执行；
6. 固定轮数时每 worker 恰好 `STRESS_FIXED_ROUNDS` 轮；每轮 resurrections=2、cleanups=7；`started==finished`；
7. 收尾 goroutine 回基线（+4 容差）；最后一行 `STRESS PASS x14_weak_resurrect`。

## 参数

| 变量 | 默认 | 说明 |
|---|---|---|
| `STRESS_SECONDS` | 60 | 运行时长；`0`=固定轮数 |
| `STRESS_WORKERS` | 2 | worker 数（≤16） |
| `STRESS_HEARTBEAT` | 5 | 心跳间隔（秒），6 个周期无进展 → FAIL |
| `UAF_SEED` | 1 | 种子，失败行带出 |
| `STRESS_FIXED_ROUNDS` | 8 | `seconds=0` 时每 worker 轮数 |
| `STRESS_CRASH_FILE` | runner 注入 | `debug.SetCrashOutput` 落盘路径 |

轮询/等待上限固定 15s，仅作为“终结器/cleanup 未执行”的失败上限，不作性能断言。

## 失败判读

- `phase=weak.plain/interior ... 强引用持有时 weak.Value()==nil`：对象仍被强引用却被回收（漏标/提前释放，最直接的 UAF 判据）；
- `失活对象 weak.Value() 未在超时内转 nil`：对象泄漏/未回收（检查是否有隐藏强引用或 finalizer 未清）；
- `phase=resurrect.gen1/gen2 ...`：复活后对象损坏、终结次数/顺序错误、终结器内 `SetFinalizer` 未生效；
- `未再注册仍发生第三次终结`：finalizer 重复武装；
- `cleanup.multi ... Stop 的 cleanup 仍执行` / `执行集合=...`：Stop 失效、重复执行或参数错；
- `cleanup.interleaved: B 仍强持有却执行 cleanup`：cleanup 提前触发（对象仍可达）；
- `强持有期间执行 cleanup/finalizer`：可达性/特殊对象处理错误；
- `anchor 被终结`：可达对象 finalizer 误触发（finale/checkfinalizers 下应直接 throw）；
- `finalizer gen=... 未在超时内执行`：终结器饥饿/丢失；
- `goroutines baseline ... leak`：cleanup/finalizer 派生的 goroutine 泄漏；
- `no progress`：watchdog 超时。

## 运行

```bash
SUITE=stress FILTER=x14 STRESS_SECONDS=3 PROFILES=soak,window,finale REPEAT=1 TIMEOUT=90 ./run.sh
SUITE=stress FILTER=x14 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=4 PROFILES=base,clobberfree,detector ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=x14 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=2 PROFILES=base ./run.sh
```

## 来源

- `docs/research/stress_extreme_survey.md` x14、§3.2/3.4（finale profile、weak 探针、AddCleanup）；
- `runtime/mfinal_test.go:24-115,218-257`、`runtime/mcleanup_test.go:18-410`、`runtime/testdata/testprog/checkfinalizers.go:1-102`；
- `src/weak/pointer.go:1-88`（强持有非 nil / drop 后 nil / finalizer 排队即 nil 的语义）、`runtime/mcleanup.go:1-180`（多 cleanup、Stop、顺序未定义）；
- 复活的 `SetFinalizer` 再武装语义见 `runtime/mfinal.go:343-355`；n03 只做生命周期，本场景不重复其“可达不终结”主路径。
