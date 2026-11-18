---
id: s05_generated_code_soak
priority: P1
covers: [L10, L06, L11, L17]
requires: []
default_seconds: 60
max_workers: 32
---

# 场景

生成代码形态长跑。`generated.go` 由固定脚本 `tools/gen_stress_code.py` 生成（约 9.6k 行）：64 个 protobuf/ORM 形态 record 类型（string/int/`[]*Payload` 指针字段）、Record 接口与 getter/setter、深拷贝函数、一个 1024 分支 dispatch switch、枚举与描述表。

多 worker 每轮构造一条 record，依次执行：接口调用 → 1024 分支 dispatch → `encoding/json` marshal → 显式 `runtime.GC()` → unmarshal → 显式 `runtime.GC()` → 校验 → 深拷贝 → 变异原对象验证副本独立 → 环形保留 3 条副本。24 轮为周期：warmup(2) → regrow(6) → steady(16) → shrink(1，丢弃旧保留 + `debug.FreeOSMemory`)。

## 不变量

1. 每个 kind 的枚举名/描述非空，record 工厂非 nil 且 `Kind()` 与请求一致；
2. `CheckPayload`：`[]*Payload`、`Leader`、嵌套 `Children` 的 ID/Key/Value/Weight/Data 与构造公式一致；
3. JSON roundtrip 后 `Checksum()`、ID/Name/Tags 与编码前一致，dispatch 结果一致；
4. 深拷贝副本与原对象不共享任何指针字段（payload、children、leader）；
5. 变异原对象（payload/child/tag）后原对象校验和改变、副本校验和不变；
6. 保留副本跨轮次、跨 `FreeOSMemory` 后校验和与指针内容不变；
7. 心跳持续增长，watchdog 无停滞；
8. 结束打印 `STRESS PASS s05_generated_code_soak`。

## 参数

- `STRESS_SECONDS`（默认 60，0=固定轮数）
- `STRESS_WORKERS`（默认 4，最大 32）
- `STRESS_HEARTBEAT`（默认 5s，停滞 6 个周期判失败）
- `UAF_SEED`（默认 1）
- `STRESS_FIXED_ROUNDS`（默认 32，仅 `STRESS_SECONDS=0` 时生效）

## 失败判读

- `roundtrip checksum/fields`：JSON 编解码丢失或破坏字段；
- `payload[...] corrupted` / `retained checksum`：指针字段被回收、内容破坏或零值/复制未初始化；
- `deep copy shares pointer fields` / `deep copy is not independent`：深拷贝浅拷贝化或写屏障/复制错误；
- `dispatch not deterministic`：大 switch 分支执行错误；
- `enum table broken` / `factory broken`：生成表或取模索引错误；
- `watchdog stalled`：死锁/GC 饥饿；
- 只在 clobberfree/efence 下失败：UAF。

## 运行

```bash
# 重新生成（固定脚本，无随机；gofmt 后入库）
python3 tools/gen_stress_code.py

SUITE=stress FILTER=s05 STRESS_SECONDS=60 PROFILES=soak ./run.sh
SUITE=stress FILTER=s05 STRESS_SECONDS=0 STRESS_FIXED_ROUNDS=20 PROFILES=base ./run.sh
```
