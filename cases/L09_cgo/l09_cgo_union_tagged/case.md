---
id: l09_cgo_union_tagged
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:46（MinIO 覆盖 L09，C 库联合体/紧凑布局）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L09，tagged union 常见于 C 数据结构）
tags: [L09, cgo, union, tagged-union, raw-alias, offsetof, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库的 tagged union（variant）：`val_t{ uint32_t tag; union { int64_t i; double d; struct{int32_t x,y;} pt; uint8_t raw[8]; } u; }`。cgo 不能直接访问 union 成员，本用例全部经 C 辅助函数构造/读取，覆盖 tag 分派、`int64/double` 的 little-endian raw 别名、point 双字段、raw 字节视图、`sizeof`/`offsetof` 与 Go 侧布局一致性、运行期切换活动成员、以及跨多轮 GC 的稳定性。C 不保留 Go 指针。

# 期望行为

1. `sizeof(val_t)`、`offsetof(val_t,u)` 与 C 辅助函数返回值一致；
2. C 填充的 12 元素数组逐个 tag/值/raw 视图正确，C 求和与 Go 参考一致；
3. 四种 tag（INT/DBL/PT/RAW）各 3 个，C 计数正确；
4. Go 经 C setter 构造的数组可被 C 正确求和，值逐项正确；
5. 切换活动成员时 tag 同步变化，旧成员不再作为当前视图使用；
6. raw 越界/负索引返回 -1；
7. 8 轮显式 GC 后所有 tag/值/求和稳定；
8. 最后一行 `ALL PASS l09_cgo_union_tagged`。

# 失败判读

- `sizeof/offsetof`：cgo 对 union 的翻译布局与 C 不一致；
- `int raw/double raw`：union 别名或小端位模式错误（仅在 C 报告小端时断言）；
- `point[..]`：union 内嵌 struct 字段偏移/读写错误；
- `tag[..]`：tag 与活动成员不同步，或 C 数组越界；
- `C tagged union sum`：switch 分派或 union 读写路径错误；
- GC 后失败：C 保留了 Go 指针（本用例数组在 Go 堆/栈，C 仅调用期内写）；
- 构建失败：preamble 中缺少 `stddef.h`（`offsetof`）。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_union_tagged
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
