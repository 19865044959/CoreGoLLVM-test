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
| u04_unsafe_zerocopy_buffer | P0 | L07, L17, L18 |  | 60 | 32 |
| u05_pool_identity_reuse | P0 | L05, L18 |  | 60 | 32 |
| u06_syscall_io_fs | P1 | L19, L05 | unix | 60 | 16 |
| u07_goroutine_spawn_churn | P0 | L04, L17 |  | 60 | 16 |
| u08_panic_stack_interact | P1 | L17, L04 |  | 60 | 8 |
| u09_stack_shrink_cycles | P0 | L04, L17 |  | 60 | 8 |
| u10_multi_gen_config | P1 | L02, L18 |  | 60 | 32 |
| u11_timer_heap_churn | P0 | L19, L04 |  | 60 | 32 |
| u12_map_grow_stress | P0 | L13, L18 |  | 60 | 16 |
| x01_aggregate_statepoint | P0 | L17, L05 |  | 60 | 16 |
| x02_localarray_liveness | P0 | L17, L11 |  | 60 | 16 |
| x03_namedret_defer_rvo | P1 | L17, L04 |  | 60 | 16 |
| x04_lockosthread_stw | P1 | L04, L17 |  | 60 | 32 |
| x05_goexit_panicframe | P0 | L17, L18 |  | 60 | 16 |
| x06_coro_iterpull | P0 | L01, L16, L17 |  | 60 | 16 |
| x07_cgo_panic_deepstack | P0 | L09, L05, L17, L04 | cgo | 60 | 8 |
| x08_signal_altstack | P1 | L09, L19, L17, L04 | cgo | 60 | 8 |
| x09_asm_abi0_stackmap | P0 | L08, L17, L05 | asm, amd64 | 60 | 32 |
| x10_inline_tailcall_pcdata | P0 | L17, L11 |  | 60 | 16 |
| x13_safepoint_matrix | P0 | L17, L18, L05 |  | 60 | 12 |
| x14_weak_resurrect | P1 | L18 |  | 60 | 16 |

## 覆盖维度计数

- L01: 2
- L02: 9
- L03: 2
- L04: 12
- L05: 19
- L06: 4
- L07: 1
- L08: 1
- L09: 3
- L10: 1
- L11: 3
- L13: 8
- L14: 1
- L16: 2
- L17: 23
- L18: 16
- L19: 8

共 42 个 stress 场景。
