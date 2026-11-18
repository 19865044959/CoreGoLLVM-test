# stress 压测矩阵（自动生成）

> 由 `tools/gen_stress_matrix.py` 扫描 `stress/*/scenario.md` 生成；不占 19 维 unit 配额。

| 场景 | 优先级 | 覆盖维度 | requires | 默认时长 | max_workers |
|---|---|---|---|---:|---:|
| s01_service_soak | P1 | L02, L05, L06, L17, L18, L19 |  | 60 | 64 |
| s02_heap_cycle_soak | P1 | L05, L13, L18 |  | 60 | 32 |
| s03_stack_churn_soak | P1 | L04, L17 |  | 60 | 16 |
| s04_preempt_storm_soak | P0 | L03, L02 |  | 60 | 32 |
| s05_generated_code_soak | P1 | L10, L06, L11, L17 |  | 60 | 32 |
| s06_queue_pipeline_soak | P1 | L02, L13, L19, L17 |  | 60 | 32 |

## 覆盖维度计数

- L02: 3
- L03: 1
- L04: 1
- L05: 2
- L06: 2
- L10: 1
- L11: 1
- L13: 2
- L17: 4
- L18: 2
- L19: 2

共 6 个 stress 场景。
