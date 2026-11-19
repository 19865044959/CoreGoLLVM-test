---
id: n12_fuzz_decoder
priority: P2
covers: [L05, L06, L13]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

固定 seed 变异器 + 多解码器长跑。启动时自生成 7 类初始语料（JSON/XML/gob/gzip/tar/zip/png）；每个 worker 为每类维护当前输入，用 xorshift64 变异器循环变异：

- 字节翻转 / 随机覆写 / 截断 / 插入随机字节 / 删除片段 / 片段复制 / 跨语料拼接（1–4 个操作）；
- 输入硬上限 64KB；每 16 次同类解码回到初始语料，每 32 次做一次未变异语料解码，保证每个 decoder 都有成功路径；
- 解码调用逐个 `recover`：panic 只按 decoder 计数并打印 panic rate，不做门限断言；
- 结构预算：JSON/XML 嵌套深度 ≤128（超出跳过）、gzip 解压 ≤512KB、tar/zip 条目 ≤64 且解包 ≤512KB、PNG 预检 IHDR 尺寸（≤1024×1024 且 ≤1M 像素），防止变异输入导致无界分配；
- 成功解码后：JSON 做 `Marshal → Unmarshal → DeepEqual`，gob 做 `Decode → Encode → Decode → 逐字段比较`；不一致立即 FAIL；
- 独立 GC churn 与输入保留环制造对象图压力；watchdog 检测解码死循环/卡死。

## 不变量

1. 任何输入长度 ≤ 65536，`max_input_seen` 超限即 FAIL。
2. 每个 decoder 都被实际喂过输入；总解码数 > 0。
3. JSON/gob 成功解码后的 roundtrip 必须成功且值一致；`roundtrip mismatch` 立即 FAIL。
4. 解码 panic 只记录（按 decoder 分类统计），不视为失败；未知 decoder 或语料为空立即 FAIL。
5. 每次解码只调用一次，结构预算保证单次解码有界（深度/条目/解压字节/像素），心跳随每次解码增长；6×H 无心跳说明 decoder 卡死，watchdog FAIL 并打印所有 goroutine 栈。
6. 结束时 goroutine 回到基线 +2 内；打印总 metrics、逐 decoder 明细与 `STRESS PASS n12_fuzz_decoder`。

## 参数

- `STRESS_SECONDS`（默认 60，`0`=固定轮数模式）
- `STRESS_WORKERS`（默认 4，上限 32）
- `STRESS_HEARTBEAT`（默认 5s）
- `UAF_SEED`（默认 1，决定变异序列）
- `STRESS_FIXED_ROUNDS`（默认 24，仅 `STRESS_SECONDS=0` 时生效；每 worker 的轮数，每轮 32 次解码）

## 失败判读

- `roundtrip mismatch` / `decode re-encoded value`：成功解码后的语义或编解码器状态损坏；
- `empty corpus for ...`：语料自生成失败；
- `decoder ... exercised actual=0`：某些 decoder 未被覆盖；
- `watchdog stalled ... possible decoder hang`：解码器死循环或锁死（含 goroutine 栈）；
- `max_input_seen` 超限：变异器长度控制失效；
- 仅在 clobberfree/efence 下失败：UAF；
- panic 率大幅变化只作观测：长跑中记录并按 decoder 打印，不单独判失败。

## 运行

```bash
SUITE=stress FILTER=n12 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=n12 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
BUILD_FLAGS=-race SUITE=stress FILTER=n12 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
SUITE=stress FILTER=n12 STRESS_SECONDS=3 PROFILES=soak,soak-single REPEAT=1 ./run.sh
```

## 来源

- `go/src/encoding/json/fuzz_test.go:15,55`（JSON fuzz 入口）
- `go/src/archive/zip/fuzz_test.go:15`（zip fuzz 入口）
- `go/src/encoding/gob/codec_test.go:1423-1514`（gob 截断/错误输入）
- <https://go.dev/doc/fuzz/>（固定语料 + 变异 + 输入预算）
