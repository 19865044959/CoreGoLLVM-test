# stress 压测矩阵（自动生成）

> 由 `tools/gen_stress_matrix.py` 扫描 `stress/*/scenario.md` 生成；不占 19 维 unit 配额。

| 场景 | 优先级 | 覆盖维度 | requires | 默认时长 | max_workers |
|---|---|---|---|---:|---:|
| n01_timer_netpoll_storm | P0 | L19, L04, L02 |  | 60 | 32 |
| n02_map_table_churn | P0 | L13, L18, L06 |  | 60 | 32 |
| n03_gc_lifecycle_finalizer | P0 | L18, L05, L13 |  | 60 | 32 |
| n04_write_barrier_graph | P0 | L18, L05, L17 |  | 60 | 32 |
| n05_trace_pprof_metrics | P1 | L04, L05, L14 |  | 60 | 16 |
| n06_exec_signal_lifecycle | P1 | L02, L04, L19 |  | 60 | 8 |
| n07_cgo_storm | P1 | L05, L09, L17 | cgo | 60 | 8 |
| n08_parser_typecheck | P1 | L01, L05, L16 |  | 60 | 8 |
| n09_lsm_storage | P2 | L02, L05, L13 |  | 60 | 32 |
| n10_mvcc_watch_lease | P2 | L02, L05, L13, L18 |  | 60 | 32 |
| n11_http_tls_conn | P1 | L02, L05, L19 |  | 60 | 64 |
| n12_fuzz_decoder | P2 | L05, L06, L13 |  | 60 | 32 |
| s01_service_soak | P1 | L02, L05, L06, L17, L18, L19 |  | 60 | 64 |
| s02_heap_cycle_soak | P1 | L05, L13, L18 |  | 60 | 32 |
| s03_stack_churn_soak | P1 | L04, L17 |  | 60 | 16 |
| s04_preempt_storm_soak | P0 | L03, L02 |  | 60 | 32 |
| s05_generated_code_soak | P1 | L10, L06, L11, L17 |  | 60 | 32 |
| s06_queue_pipeline_soak | P1 | L02, L13, L19, L17 |  | 60 | 32 |
| u01_wb_boundary_storm | P0 | L18, L05, L17 |  | 60 | 32 |
| u02_preempt_scan_window | P0 | L03, L17, L18 |  | 60 | 32 |
| u03_gc_assist_pressure | P0 | L05, L18 |  | 60 | 32 |

## 覆盖维度计数

- L01: 1
- L02: 8
- L03: 2
- L04: 4
- L05: 13
- L06: 4
- L09: 1
- L10: 1
- L11: 1
- L13: 7
- L14: 1
- L16: 1
- L17: 8
- L18: 9
- L19: 5

共 21 个 stress 场景。
