# 用例覆盖矩阵（自动生成）

> 由 `tools/gen_matrix.py` 扫描 `cases/*/*/case.md` 生成；不要手改。

## 维度汇总

| 维度 | 名称 | 用例数 | P0 | P1 | P2 | 目标 | 状态 |
|---|---|---:|---:|---:|---:|---:|---|
| L01_generics | L1 泛型 | 50 | 16 | 18 | 16 | 50 | ✅ |
| L02_concurrency | L2 高并发 | 50 | 10 | 23 | 17 | 50 | ✅ |
| L03_async_preempt | L3 异步抢占 | 50 | 8 | 26 | 16 | 50 | ✅ |
| L04_scheduler_stack | L4 协程调度/栈 | 50 | 8 | 28 | 14 | 50 | ✅ |
| L05_alloc_gc | L5 大量内存分配 | 50 | 8 | 27 | 15 | 50 | ✅ |
| L06_reflect_iface | L6 反射与接口 | 50 | 8 | 27 | 15 | 50 | ✅ |
| L07_unsafe_zerocopy | L7 unsafe 与零拷贝 | 50 | 13 | 22 | 15 | 50 | ✅ |
| L08_asm_simd | L8 汇编与 SIMD | 50 | 0 | 50 | 0 | 50 | ✅ |
| L09_cgo | L9 cgo | 50 | 0 | 50 | 0 | 50 | ✅ |
| L10_codegen | L10 代码生成 | 50 | 7 | 27 | 16 | 50 | ✅ |
| L11_compiler_opts | L11 编译期优化 | 50 | 15 | 21 | 14 | 50 | ✅ |
| L12_init_link | L12 初始化与链接 | 50 | 9 | 26 | 15 | 50 | ✅ |
| L13_runtime_ds | L13 运行时数据结构 | 50 | 16 | 21 | 13 | 50 | ✅ |
| L14_debug_info | L14 调试信息 | 50 | 3 | 32 | 15 | 50 | ✅ |
| L15_build_tags | L15 构建标签与多平台 | 50 | 6 | 35 | 9 | 50 | ✅ |
| L16_template_interp | L16 模板/解释器/元编程 | 50 | 9 | 26 | 15 | 50 | ✅ |
| L17_stackmap | L17 栈映射 | 50 | 17 | 21 | 12 | 50 | ✅ |
| L18_gc_lifecycle_wb | L18 GC 生命周期与写屏障 | 50 | 10 | 27 | 13 | 50 | ✅ |
| L19_netpoll_timer_signal | L19 netpoll/定时器/信号 | 50 | 6 | 29 | 15 | 50 | ✅ |
| **合计** | | **950** | | | | **950** | |

## L01_generics — L1 泛型（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l01_box_pointer_gc | P0 | generics | L01, generics, pointer-field, instantiation, gc, write-barrier | 3 |
| l01_constrained_stack | P0 | generics | L01, generics, constrained-stack, numeric-constraint, running-min-max, pointer-payload, gc, UAF_SEED | 3 |
| l01_constraint_methods | P0 | generics | L01, generics, type-constraint, methods, comparable, union, pointer-receiver, gc, UAF_SEED | 3 |
| l01_constraint_union | P0 | generics | L01, generics, type-constraint, union, tilde, pointer-payload, gc | 3 |
| l01_error_result_union | P1 | generics | L01, generics, error-union, result-type, errors.Is, errors.As, errors.Join, multi-type-params, gc | 4 |
| l01_generic_alg_edges | P1 | generics | L01, generics, algorithm-boundary, empty-single-all-equal, sort-search, large-input, gc, UAF_SEED | 3 |
| l01_generic_avl_tree | P1 | generics | L01, generics, AVL, tree, ordered-constraint, rotation, in-order, deletion, gc, UAF_SEED | 3 |
| l01_generic_bloom | P1 | generics | L01, generics, bloom-filter, bitset, double-hashing, false-positive, clone, union, gc, UAF_SEED | 2 |
| l01_generic_comparator | P1 | generics | L01, generics, comparator, stable-sort, container/heap, multi-key, pointer-elements, gc, UAF_SEED | 4 |
| l01_generic_const_bounds | P0 | generics | L01, generics, type-constraint, numeric-boundary, overflow-wrap, custom-underlying-type, gc | 3 |
| l01_generic_container_tower | P1 | generics | L01, generics, nested-instantiation, container-composition, pointer-identity, gc, UAF_SEED | 3 |
| l01_generic_csv_codec | P0 | generics | L01, generics, csv, encoding-csv, delimiter-detection, quoting, roundtrip, errors, gc, UAF_SEED | 3 |
| l01_generic_deep_clone | P1 | generics | L01, generics, deep-clone, aliasing-isolation, nil-empty-semantics, recursive-copy, gc, UAF_SEED | 3 |
| l01_generic_deep_recursion | P2 | generics | L01, generics, deep-recursion, stack-growth, generic-frame, pointer-liveness, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_di_container | P1 | generics | L01, generics, dependency-injection, container, singleton, transient, cycle-detection, type-assertion, gc, UAF_SEED | 2 |
| l01_generic_diff_patch | P2 | generics | L01, generics, diff, patch, LCS, unified-diff, reverse-patch, error-path, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l01_generic_error | P1 | generics | L01, generics, generic-error, error-chain, errors.Is, errors.As, errors.Join, gc | 3 |
| l01_generic_event_bus | P2 | generics | L01, generics, event-bus, subscribe, unsubscribe, ordering, snapshot-dispatch, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_fsm | P0 | generics | L01, generics, fsm, state-machine, comparable, transition-table, history, rejection, gc, UAF_SEED | 3 |
| l01_generic_functional_options | P0 | generics | L01, generics, functional-options, defaults, nil-option, option-chain, config, gc, UAF_SEED | 3 |
| l01_generic_future | P1 | generics | L01, generics, future, promise, once, broadcast, map-chain, all, error-propagation, gc, UAF_SEED | 3 |
| l01_generic_graph | P2 | generics | L01, generics, graph, BFS, DFS, topo-sort, cycle-detection, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_graph_clone | P2 | generics | L01, generics, graph, deep-copy, aliasing, cycles, payload-clone, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_heap_pq | P2 | generics | L01, generics, heap, priority-queue, indexed-heap, handle-update, k-way-merge, pointer-identity, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l01_generic_iface_closure | P1 | generics | L01, generics, generic-interface, closure-state, middleware-chain, method-value, typed-nil, gc | 3 |
| l01_generic_inst_dedup | P1 | generics | L01, generics, instantiation-dedup, type-identity, composite-type, function-value, concurrent-first-use, gc | 3 |
| l01_generic_interface | P0 | generics | L01, generics, generic-interface, interface-embedding, delegation, method-value, pointer-identity, gc | 3 |
| l01_generic_interval_tree | P2 | generics | L01, generics, interval-tree, treap, augmented-tree, stab-query, intersects, pointer-identity, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l01_generic_json_envelope | P0 | generics | L01, generics, json, serialization, nil-vs-empty, custom-marshaler, strict-decode, gc, UAF_SEED | 3 |
| l01_generic_map_cache | P1 | generics | L01, generics, map-container, LRU, eviction, hit-miss, pointer-identity, gc, UAF_SEED | 3 |
| l01_generic_matrix | P2 | generics | L01, generics, matrix, numeric-constraint, transpose, multiply, algebra-identity, gc, TORTURE_ROUNDS, UAF_SEED | 2 |
| l01_generic_methods | P0 | generics | L01, generics, methods, generic-interface, embedding, dispatch, gc | 3 |
| l01_generic_object_pool | P2 | generics | L01, generics, object-pool, sync.Mutex, reset, reuse, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_outcome_union | P0 | generics | L01, generics, tagged-union, outcome, retry, skip, fold, value-invariant, gc, UAF_SEED | 3 |
| l01_generic_pipeline | P2 | generics | L01, generics, pipeline, batching, channel, ordering, fail-fast, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_queue_stack | P0 | generics | L01, generics, queue, stack, ring-buffer, callback, pointer-identity, gc | 3 |
| l01_generic_ringbuf | P2 | generics | L01, generics, ring-buffer, overwrite, wraparound, power-of-two, snapshot, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_search | P2 | generics | L01, generics, sort.Slice, sort.Search, binary-search, pointer-elements, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_set_algebra | P0 | generics | L01, generics, set-algebra, union, intersect, difference, symmetric-difference, ordered-set, pointer-identity, gc, UAF_SEED | 3 |
| l01_generic_sharded_map | P2 | generics | L01, generics, sync.RWMutex, sharded-container, concurrent-container, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_slice_ops | P1 | generics | L01, generics, slice-utils, map-filter-reduce, chunk, distinct, group-by, flat-map, zip, ordering, gc, UAF_SEED | 4 |
| l01_generic_sync_container | P2 | generics | L01, generics, sync.RWMutex, concurrent-container, pointer-values, gc, TORTURE_ROUNDS | 3 |
| l01_generic_trie | P2 | generics | L01, generics, trie, nested-map, prefix-query, ordered-walk, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_ttl_lru | P1 | generics | L01, generics, TTL, LRU, container-list, fake-clock, eviction, hit-miss, pointer-identity, gc, UAF_SEED | 4 |
| l01_generic_variadic | P0 | generics | L01, generics, variadic, slice-expansion, pointer-elements, gc | 3 |
| l01_generic_worker_pool | P2 | generics | L01, generics, worker-pool, goroutine, channel, WaitGroup, deterministic-results, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_instantiation_reuse | P1 | generics | L01, generics, instantiation-identity, type-identity, reflect, gc | 3 |
| l01_lru_segmented | P1 | generics | L01, generics, LRU, SLRU, segmented-cache, eviction, reference-model, pointer-identity, gc, UAF_SEED | 3 |
| l01_nested_generics | P0 | generics | L01, generics, nested-instantiation, pointer-chain, multi-type-params, gc | 3 |
| l01_slice_map_generic | P1 | generics | L01, generics, slice-growth, map-container, pointer-elements, gc | 3 |

## L02_concurrency — L2 高并发（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l02_actor_mailbox_state | P1 |  | L02, actor, mailbox, channel, single-owner, fifo-command, close-drain, gc, UAF_SEED | 3 |
| l02_actor_supervision_restart | P1 |  | L02, actor, supervision, panic-recover, restart-budget, mailbox, escalation, gc, UAF_SEED | 3 |
| l02_atomic_batch_snapshot | P2 |  | L02, atomic.Pointer, CAS, immutable-snapshot, batch-atomicity, map-clone, generation, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_atomic_map_swap | P2 |  | L02, atomic-counter, map-swap, atomic.Pointer, immutable-snapshot, generation, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_atomic_pointer_config | P1 |  | L02, atomic.Value, hot-reload, immutable-config, pointer, generation, gc | 2 |
| l02_atomic_ring_mpmc | P2 |  | L02, atomic, ring-buffer, mpmc, sequence-counter, lock-free, checksum, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_backpressure_deadline | P1 |  | L02, backpressure, bounded-queue, context-deadline, DeadlineExceeded, deterministic-expiry, accounting, gc, UAF_SEED | 3 |
| l02_barrier_phase_gate | P1 |  | L02, barrier, phase-gate, generation, break, broadcast, gc, UAF_SEED | 3 |
| l02_bounded_queue_backpressure | P2 |  | L02, bounded-queue, backpressure, sync.Cond, producer-consumer, gc, TORTURE_ROUNDS | 3 |
| l02_bounded_queue_cancel | P2 |  | L02, bounded-queue, context-cancel, close-wakeup, drain, blocked-producer, blocked-consumer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_broadcast_fanin_acks | P1 |  | L02, fan-out, fan-in, broadcast, ack, bounded-inbox, backpressure, ordering, gc, UAF_SEED | 3 |
| l02_cancel_close_race | P2 |  | L02, context-cancel, channel-close, close-race, terminal-outcome, no-send-on-closed, accounting, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_chan_close_drain | P0 |  | L02, channel-close, drain, recover, double-close, closed-select, gc | 3 |
| l02_chan_interface_message | P1 |  | L02, channel-of-interface, type-switch, nil-interface, dynamic-type, pointer-payload, gc | 3 |
| l02_chan_ring_producers_consumers | P2 |  | L02, producer-consumer, bounded-channel, pointer-message, sync.WaitGroup, gc, check-sum, UAF_SEED, TORTURE_ROUNDS | 2 |
| l02_chan_semaphore_tokens | P1 |  | L02, channel-semaphore, token, TryAcquire, context-cancel, no-token-leak, gc, UAF_SEED | 3 |
| l02_concurrent_counter_churn | P2 |  | L02, atomic, counter, CAS-max, concurrent-alloc, interior-pointer, gc, TORTURE_ROUNDS | 3 |
| l02_concurrent_map_grow | P2 |  | L02, sharded-map, resize, rehash, concurrent-growth, rwlock, monotonic-count, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_context_cancel_fanout | P1 |  | L02, context, WithCancel, fanout, broadcast, shutdown, gc | 2 |
| l02_context_cause_chain | P0 |  | L02, context, WithCancelCause, WithDeadlineCause, WithTimeoutCause, cause-propagation, first-cause-wins, gc, UAF_SEED | 3 |
| l02_context_deadline_tree | P0 |  | L02, context, deadline-tree, WithCancel, WithDeadline, propagation, DeadlineExceeded, gc | 3 |
| l02_context_value_propagation | P0 |  | L02, context, WithValue, propagation, shadowing, sibling-isolation, cancel-survival, nil-key, gc, UAF_SEED | 3 |
| l02_countdown_latch_gate | P0 |  | L02, countdown-latch, one-shot, gate, broadcast, idempotent, gc, UAF_SEED | 3 |
| l02_ctx_timeout_prodcons | P1 |  | L02, context, WithCancel, WithDeadline, DeadlineExceeded, producer-consumer, residual-drain, gc | 3 |
| l02_double_close_recover | P0 |  | L02, double-close, recover, idempotent-shutdown, sync.Once, nil-channel, closed-send, gc | 3 |
| l02_errgroup_first_error | P1 |  | L02, errgroup, first-error, context-cancel, WaitGroup, sync.Once, gc, UAF_SEED | 3 |
| l02_fanin_fanout_two_level | P2 |  | L02, fan-in, fan-out, two-level, WaitGroup, close-cascade, checksum, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_flow_credits_backpressure | P2 |  | L02, credits, flow-control, backpressure, window-update, blocked-detection, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_lock_chan_hybrid | P1 |  | L02, mutex, channel-notify, lost-wakeup, fifo, coalesced-wakeup, close-drain, gc, UAF_SEED | 3 |
| l02_lru_sharded_concurrent | P2 |  | L02, LRU, sharded-lock, container/list, recency-promotion, eviction, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_lru_weighted_bytes | P2 |  | L02, LRU, weighted-capacity, eviction-order, container/list, concurrent-cache, eviction-accounting, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_once_barrier_trip | P0 |  | L02, one-shot-barrier, trip, leader-election, broadcast, memory-visibility, late-waiter, gc, UAF_SEED | 3 |
| l02_once_registry_init | P1 |  | L02, sync.Once, nested-once, registry, duplicate-registration, concurrent-lookup, panic-once, gc | 3 |
| l02_ordered_fanout_sequence | P2 |  | L02, fan-out, ordering, sequencer, reorder-buffer, shuffle, checksum, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_pipeline_stage_fence | P1 |  | L02, pipeline, stage-fence, bounded-channel, multi-stage, backpressure-drain, checksum, gc, UAF_SEED | 3 |
| l02_pubsub_broker_drop | P2 |  | L02, pubsub, broker, slow-consumer, nonblocking-drop, replay, unsubscribe-close, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_rwlock_upgrade_snapshot | P1 |  | L02, RWMutex, lock-upgrade, double-check, immutable-value, snapshot, version, gc, UAF_SEED | 3 |
| l02_rwmutex_cache_evict | P1 |  | L02, RWMutex, cache, LRU, eviction, pinned-entry, concurrent-readers, gc, UAF_SEED | 3 |
| l02_rwmutex_shared_map | P1 |  | L02, RWMutex, shared-map, pointer-record, snapshot, gc | 3 |
| l02_rwmutex_struct_slice | P1 |  | L02, RWMutex, struct-slice, snapshot-copy, in-place-update, gc, UAF_SEED | 3 |
| l02_select_fairness | P1 |  | L02, select, fairness, starvation, closed-channel, nil-channel, default, gc, UAF_SEED | 3 |
| l02_select_multiplex_close | P0 |  | L02, select, channel-close, drain, multiplex, pointer-message, gc | 2 |
| l02_semaphore_ctx_panic | P1 |  | L02, semaphore, context-cancel, panic-recover, deferred-release, token-conservation, peak, gc, UAF_SEED | 3 |
| l02_semaphore_weighted_ctx | P1 |  | L02, semaphore, weighted, fifo-waiters, context-cancel, TryAcquire, gc, UAF_SEED | 3 |
| l02_singleflight_dedupe | P0 |  | L02, singleflight, dedupe, shared-result, error-not-cached, waitgroup, gc, UAF_SEED | 3 |
| l02_sync_once_pool_init | P1 |  | L02, sync.Once, sync.Pool, concurrent-init, reuse, reset, gc | 3 |
| l02_syncmap_pointer_cache | P2 |  | L02, sync.Map, pointer-cache, LoadOrStore, Range, generation, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_waitgroup_pipeline | P1 |  | L02, WaitGroup, pipeline, channel, pointer-payload, stage-close, gc | 2 |
| l02_work_stealing_deque | P2 |  | L02, work-stealing, deque, LIFO-owner, FIFO-thief, mutex, checksum, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_worker_pool_quit | P0 |  | L02, worker-pool, quit-channel, WaitGroup, peak-concurrency, barrier, gc | 3 |

## L03_async_preempt — L3 异步抢占（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l03_append_growth_loop | P2 |  | L03, async-preempt, append, slice-growth, pointer-slice, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_atomic_pointer_snapshot | P1 |  | L03, async-preempt, atomic.Pointer, CAS, snapshot, pointer-liveness, gc, UAF_SEED | 2 |
| l03_atomic_spin_loop | P0 |  | L03, async-preempt, atomic, spin-loop, gc, preemption | 2 |
| l03_atomic_value_snapshot | P1 |  | L03, async-preempt, atomic.Value, store-load, immutable-snapshot, reader-loop, pointer-liveness, gc, UAF_SEED | 2 |
| l03_big_stack_local | P2 |  | L03, async-preempt, large-stack-frame, stack-growth, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_big_struct_copy_loop | P2 |  | L03, async-preempt, large-struct, value-copy, memmove, stack-map, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_big_switch_loop | P0 |  | L03, async-preempt, big-switch, string-switch, tight-loop, pointer-hold, gc, UAF_SEED | 2 |
| l03_branch_switch_holdptr | P0 |  | L03, async-preempt, switch-branch, pointer-liveness, gc, UAF_SEED | 2 |
| l03_build_loop_gc | P2 |  | L03, async-preempt, map-build, slice-build, pointer-chain, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_call_each_round_stack | P2 |  | L03, async-preempt, call-per-round, stack-growth, stack-shrink, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_chan_every_n | P1 |  | L03, async-preempt, channel-pingpong, every-n, message-pointer, gc, UAF_SEED | 2 |
| l03_chan_poll_loop | P1 |  | L03, async-preempt, non-blocking-channel, select-default, polling-loop, gc, UAF_SEED | 2 |
| l03_copy_loop_memmove | P2 |  | L03, async-preempt, memmove, copy-loop, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_defer_capture_in_loop | P1 |  | L03, async-preempt, defer-in-loop, closure-capture, per-iteration, LIFO, pending-defer, gc, UAF_SEED | 3 |
| l03_defer_closure_chain | P1 |  | L03, async-preempt, defer, closure, recursion, LIFO, pointer-capture, gc, UAF_SEED | 2 |
| l03_defer_each_round | P1 |  | L03, async-preempt, defer-per-round, recover, defer-record, pointer-capture, gc, UAF_SEED | 2 |
| l03_divmod_index_loop | P0 |  | L03, async-preempt, division, modulo, computed-index, gc, UAF_SEED | 2 |
| l03_funcptr_call_loop | P1 |  | L03, async-preempt, function-pointer, indirect-call, safe-point, gc, UAF_SEED | 2 |
| l03_gc_every_n_rounds | P2 |  | L03, async-preempt, gc-every-n, round-counter, tight-loop, safe-point, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_gc_interval_lcg_loop | P2 |  | L03, async-preempt, gc-every-n, variable-period, lcg-schedule, in-loop-gc, safepoint, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_heap_struct_copy_loop | P2 |  | L03, async-preempt, large-object, heap-copy, typedmemmove, write-barrier, string-header, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_iface_call_loop | P1 |  | L03, async-preempt, interface-dispatch, itab, pointer-holder, gc, UAF_SEED | 2 |
| l03_iface_closure_registry | P1 |  | L03, async-preempt, interface-dispatch, closure, itab, indirect-call, gc, UAF_SEED | 3 |
| l03_iface_multi_impl_loop | P0 |  | L03, async-preempt, interface-dispatch, multi-impl, itab, pointer-liveness, gc, UAF_SEED | 2 |
| l03_long_slice_iteration | P1 |  | L03, async-preempt, slice-iteration, pointer-array, gc, stack-map | 2 |
| l03_loop_defer_accum | P1 |  | L03, async-preempt, defer-in-loop, defer-record, pointer-capture, gc, UAF_SEED | 2 |
| l03_loop_with_calls_stack | P1 |  | L03, async-preempt, stack-growth, recursion, gc, stack-copy | 2 |
| l03_map_lookup_loop | P0 |  | L03, async-preempt, map-lookup, pointer-chase, hit-miss, gc, UAF_SEED | 2 |
| l03_map_range_gc | P1 |  | L03, async-preempt, map-range, pointer-chase, gc, UAF_SEED | 2 |
| l03_map_slice_append_loop | P1 |  | L03, async-preempt, map-of-slice, append-growth, delete-reinsert, map-growth, gc, UAF_SEED | 2 |
| l03_map_update_loop | P2 |  | L03, async-preempt, map-update, delete-reinsert, map-growth, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_mutex_lock_loop | P1 |  | L03, async-preempt, mutex, lock-unlock-loop, contention, pointer-holder, gc, UAF_SEED | 2 |
| l03_nested_goroutines | P2 |  | L03, async-preempt, nested-goroutines, tight-loop, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_nested_loops_pointers | P1 |  | L03, async-preempt, nested-loop, local-pointer, gc, UAF_SEED | 2 |
| l03_nested_select | P1 |  | L03, async-preempt, nested-select, select-default, worker-loop, gc, UAF_SEED | 2 |
| l03_panic_recover_iteration | P1 |  | L03, async-preempt, panic-recover, every-iteration, payload-interface, stack-unwind, gc, UAF_SEED | 2 |
| l03_panic_recover_loop | P1 |  | L03, async-preempt, panic, recover, stack-unwind, payload-pointer, gc, UAF_SEED | 2 |
| l03_ptr_chase_branching | P2 |  | L03, async-preempt, pointer-chase, branchy-chain, mid-chase-gc, model-check, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_ptr_chase_chain | P2 |  | L03, async-preempt, pointer-chase, linked-chain, permutation, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_rwmutex_registry_loop | P1 |  | L03, async-preempt, RWMutex, read-mostly, registry, pointer-holder, gc, UAF_SEED | 2 |
| l03_select_nested_dispatch | P1 |  | L03, async-preempt, nested-select, select-default, buffered-channel, worker-loop, gc, UAF_SEED | 2 |
| l03_select_nil_disable | P1 |  | L03, async-preempt, select, nil-channel, unbuffered-channel, deterministic-phases, gc, UAF_SEED | 2 |
| l03_select_worker_preempt | P1 |  | L03, async-preempt, select, worker-loop, tight-loop, gc, UAF_SEED | 2 |
| l03_string_bytes_convert_loop | P2 |  | L03, async-preempt, string-bytes-conversion, copy-semantics, immutability, append, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_string_ops_loop | P2 |  | L03, async-preempt, string-scan, strings-builder, substring, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_syncmap_store_loop | P2 |  | L03, async-preempt, sync.Map, store-load-delete, Range, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_ticker_tight_loop | P1 |  | L03, async-preempt, ticker, tight-loop, gc, timer | 2 |
| l03_tight_loop_no_calls | P0 |  | L03, async-preempt, tight-loop, safepoint, stack-map, gc | 2 |
| l03_tight_loop_rare_gc | P0 |  | L03, async-preempt, tight-loop, noinline, periodic-gc, safepoint | 2 |
| l03_unsafe_ptr_loop | P1 |  | L03, async-preempt, unsafe-pointer, unsafe-add, pointer-liveness, gc, UAF_SEED | 2 |

## L04_scheduler_stack — L4 协程调度/栈（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l04_big_frame_repeat | P1 |  | L04, morestack, stack-copy, big-frame, stackmap, gc, UAF_SEED | 2 |
| l04_blocked_chan_wakeup | P1 |  | L04, channel, close-wakeup, select, sudog, gc, UAF_SEED | 2 |
| l04_bounded_pool | P2 |  | L04, bounded-pool, semaphore, backpressure, worker-pool, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_close_broadcast_cascade | P0 |  | L04, close, broadcast, sync.Once, channel, gc, UAF_SEED | 2 |
| l04_cond_bounded_buffer | P1 |  | L04, sync.Cond, mutex, bounded-buffer, producer-consumer, blocking, gc, UAF_SEED | 2 |
| l04_cond_broadcast_waves | P1 |  | L04, sync.Cond, broadcast, generation, sleep-wake, gc, UAF_SEED | 2 |
| l04_cond_sleep_wake | P2 |  | L04, sync.Cond, sleep-wake, signal, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_context_cancel_cause_tree | P0 |  | L04, context, WithCancelCause, AfterFunc, cancel-tree, propagation, gc, UAF_SEED | 2 |
| l04_context_cancel_tree | P0 |  | L04, context, cancel, propagation, broadcast, blocking, gc, UAF_SEED | 2 |
| l04_cross_goroutine_handoff | P1 |  | L04, channel, handoff, ownership, producer-consumer, gc, UAF_SEED | 3 |
| l04_deep_recursion_stack | P1 |  | L04, morestack, stack-copy, recursion, gc, UAF_SEED | 2 |
| l04_gomaxprocs_read_set | P0 |  | L04, GOMAXPROCS, NumCPU, runtime, workers, gc, UAF_SEED | 2 |
| l04_goroutine_churn | P1 |  | L04, GPM, goroutine, waitgroup, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_goroutine_leak_numgor | P1 |  | L04, goroutine-leak, NumGoroutine, runtime.Stack, WaitGroup, gc, UAF_SEED | 2 |
| l04_goroutine_local_captures | P0 |  | L04, closure, capture, goroutine-local, gosched, gc, UAF_SEED | 2 |
| l04_goroutine_panic_recover | P0 |  | L04, panic, recover, defer, stack-unwind, pointer, gc, UAF_SEED | 2 |
| l04_goroutine_stack_growth | P1 |  | L04, morestack, stack-growth, stack-copy, goroutine, gc, UAF_SEED | 2 |
| l04_gosched_fairness | P1 |  | L04, Gosched, GPM, fairness, pointer, gc, UAF_SEED | 2 |
| l04_gosched_spin | P1 |  | L04, Gosched, spin, preemption, GPM, gc, UAF_SEED | 2 |
| l04_many_goroutines_big_frames | P1 |  | L04, morestack, stack-copy, big-frame, many-goroutines, gc, UAF_SEED | 3 |
| l04_mutex_blocked_gc | P1 |  | L04, mutex, rwmutex, blocking, sema, gc, UAF_SEED | 3 |
| l04_mutex_contention_loop | P1 |  | L04, mutex, contention, critical-section, gc, UAF_SEED | 2 |
| l04_mutex_convoy_queue | P1 |  | L04, mutex, convoy, waiter-queue, blocking, gc, UAF_SEED | 2 |
| l04_netpipe_blocked_io | P1 |  | L04, net.Pipe, blocking-io, netpoll, deadline, gc, UAF_SEED | 2 |
| l04_netpipe_deadline_reuse | P1 |  | L04, net.Pipe, deadline, netpoll, timeout, reuse, blocking-io, gc, UAF_SEED | 2 |
| l04_netpoll_accept_close | P1 |  | L04, netpoll, tcp, accept, deadline, blocking-io, gc, UAF_SEED | 2 |
| l04_pool_fixed_window_limit | P2 |  | L04, worker-pool, rate-limit, fixed-window, concurrency-cap, channel, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_pool_stack_growth | P1 |  | L04, worker-pool, stack-growth, morestack, stack-copy, stack-shrink, gc, UAF_SEED | 2 |
| l04_preempt_tight_loop | P2 |  | L04, preemption, tight-loop, starvation, GOMAXPROCS, async-preempt, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_scheduler_mutex_starvation | P2 |  | L04, scheduler, starvation, mutex, GOMAXPROCS, preemption, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_select_fairness_rounds | P0 |  | L04, select, fairness, channel, starvation, goroutine, gc, UAF_SEED | 2 |
| l04_select_shutdown | P2 |  | L04, select, shutdown, worker-pool, goroutine, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_semaphore_release_wake | P2 |  | L04, semaphore, waiter-queue, cancel, FIFO, wakeup, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_sleep_wake_pointer | P1 |  | L04, sleep-wake, timer, pointer, gc, UAF_SEED | 2 |
| l04_spawn_storm_exit | P1 |  | L04, goroutine, spawn-storm, immediate-exit, WaitGroup, gc, UAF_SEED | 2 |
| l04_stack_grow_interleaved_gc | P1 |  | L04, morestack, stack-growth, stack-copy, stackmap, interleaved-gc, UAF_SEED | 2 |
| l04_stack_sawtooth_cycles | P1 |  | L04, morestack, stack-copy, stack-shrink, sawtooth, recursion, gc, UAF_SEED | 2 |
| l04_stack_shrink_concurrent | P2 |  | L04, stack-shrink, stack-growth, morestack, stack-copy, concurrent, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_stack_shrink_cycles | P1 |  | L04, stack-shrink, morestack, recursion, gc, UAF_SEED | 2 |
| l04_stack_shrink_unwind | P2 |  | L04, stack-shrink, panic, recover, unwind, stack-copy, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_timer_channel_reset_reuse | P1 |  | L04, timer, Timer.Reset, Stop, channel-drain, select, timer-heap, gc, UAF_SEED | 2 |
| l04_timer_coalesce_bus | P2 |  | L04, timer, coalesce, merge, timer-heap, goroutine, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_timer_fairness | P2 |  | L04, timer, fairness, round-robin, sync.Cond, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l04_timer_heap_pointers | P2 |  | L04, timer, timer-heap, closure, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_timer_storm_deadline | P2 |  | L04, timer, AfterFunc, timer-heap, closure-pointer, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_waitgroup_close | P2 |  | L04, WaitGroup, close, producer-consumer, channel, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l04_waitgroup_reuse_rounds | P1 |  | L04, WaitGroup, reuse, Add-Wait, goroutine, gc, UAF_SEED | 2 |
| l04_waitgroup_select_done | P0 |  | L04, WaitGroup, select, done-channel, cancel, goroutine, gc, UAF_SEED | 2 |
| l04_waitgroup_wave_barrier | P1 |  | L04, WaitGroup, wave, barrier, goroutine, gc, UAF_SEED | 2 |
| l04_worker_pool_cancel_drain | P1 |  | L04, worker-pool, context-cancel, select, goroutine, gc, UAF_SEED | 2 |

## L05_alloc_gc — L5 大量内存分配（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l05_allocator_reset_pool | P1 |  | L05, sync.Pool, allocator-reset, uvarint, crc32, gc | 1 |
| l05_append_growth_gc | P0 |  | L05, append, slice-growth, copy, gc | 1 |
| l05_append_growth_ladder | P0 |  | L05, append-growth, slice-growth, old-backing, snapshot, gc | 2 |
| l05_arena_bump_blocks | P1 |  | L05, arena, bump-allocator, slab, reset-reuse, alignment, gc | 2 |
| l05_big_array_rezero | P1 |  | L05, make-zeroing, large-array, pointer-bitmap, clobberfree, span-reuse, gc | 2 |
| l05_bucket_allocator | P1 |  | L05, bucket-allocator, size-class, free-list, slab, slot-reuse, gc | 3 |
| l05_builder_pool | P1 |  | L05, sync.Pool, strings.Builder, bytes.Buffer, reset-reuse, snapshot, gc | 2 |
| l05_builder_pool_parallel | P1 |  | L05, sync.Pool, strings.Builder, concurrency, reset-reuse, gc | 2 |
| l05_builder_vs_buffer | P1 |  | L05, strings.Builder, bytes.Buffer, reset-reuse, snapshot, gc | 2 |
| l05_cap_growth_pressure | P1 |  | L05, cap-growth, slices-Grow, insert-delete, pointer-elements, clone, gc | 2 |
| l05_closure_capture_large | P1 |  | L05, closure, escape-analysis, large-object, captured-slice, gc | 3 |
| l05_closure_escape_alloc | P1 |  | L05, closure, escape-analysis, captured-slice, pointer, gc | 1 |
| l05_concurrent_compress_gc | P1 |  | L05, concurrency, compress/gzip, alloc-churn, gc | 1 |
| l05_copy_growth_overlap | P1 |  | L05, cap-growth, copy, overlapping-copy, pointer-elements, clone, gc | 2 |
| l05_gomemlimit_pressure | P2 |  | L05, GOMEMLIMIT, memory-pressure, heap-live-set, gc, torture | 2 |
| l05_goroutine_churn_alloc | P2 |  | L05, goroutine-churn, alloc-churn, concurrency, liveness, gc, torture | 3 |
| l05_goroutine_local_arena | P2 |  | L05, goroutine-local, arena, ownership-handoff, channel, liveness, gc, torture | 2 |
| l05_graph_batch_release | P2 |  | L05, object-graph, batch-release, rebuild, liveness, gc, torture | 1 |
| l05_graph_cycle_churn | P2 |  | L05, object-graph, cycles, shared-subgraph, generational-churn, liveness, gc, torture | 2 |
| l05_graph_finalizer_revival | P2 |  | L05, finalizer, object-graph, resurrection, liveness, gc, torture | 2 |
| l05_gzip_reset_churn | P1 |  | L05, compress/gzip, Writer.Reset, buffer-reuse, gc | 1 |
| l05_huge_objects_rounds | P2 |  | L05, huge-object, large-allocation, alloc-free-rounds, crc32, gc, torture | 2 |
| l05_huge_self_slice | P1 |  | L05, huge-object, sub-slice, interior-pointer, liveness, gc | 3 |
| l05_iface_box_map_bulk | P1 |  | L05, interface-boxing, map-churn, type-switch, itab, gc | 2 |
| l05_iface_boxing_pressure | P1 |  | L05, interface-boxing, any, type-switch, allocation, gc | 1 |
| l05_iface_boxing_ring | P0 |  | L05, interface-boxing, non-empty-interface, typed-nil, dispatch, gc | 3 |
| l05_iobuf_size_classes | P0 |  | L05, sync.Pool, size-class, large-object, gc | 1 |
| l05_make_zero_large | P0 |  | L05, make-zeroing, large-array, pointer-bitmap, clobberfree, span-reuse, gc | 2 |
| l05_map_bytes_churn | P2 |  | L05, map-churn, map[string][]byte, liveness, gc, torture | 1 |
| l05_map_delete_reinsert | P2 |  | L05, map-churn, delete, reinsert, liveness, gc, torture | 1 |
| l05_map_huge_value_bulk | P2 |  | L05, map-churn, huge-object, large-value, liveness, gc, torture | 2 |
| l05_map_iface_key_boxing | P1 |  | L05, map-churn, interface-key, boxing, hashing, pointer-identity, gc | 2 |
| l05_map_large_value_copy | P2 |  | L05, map-churn, large-map-value, by-value-struct, pointer-fields, copy, gc, torture | 2 |
| l05_map_small_objects | P2 |  | L05, map-churn, small-objects, pointer-fields, liveness, gc, torture | 3 |
| l05_pool_churn_gc | P0 |  | L05, sync.Pool, slice-reuse, gc | 1 |
| l05_pool_struct_slice | P1 |  | L05, sync.Pool, struct-with-slice, spans, pointer-fields, gc | 1 |
| l05_pooled_arena_slabs | P1 |  | L05, sync.Pool, arena, bump-allocator, reset-reuse, gc | 2 |
| l05_prealloc_cap_append | P0 |  | L05, make-cap, append-growth, backing-array, slice-reuse, gc | 1 |
| l05_scratch_reuse_blocks | P1 |  | L05, scratch-reuse, histogram, slice-reuse, gc | 1 |
| l05_select_loop_alloc | P0 |  | L05, select, channel, per-iteration-allocation, linked-list, liveness, gc | 2 |
| l05_select_multi_branch_alloc | P2 |  | L05, select, channel, per-iteration-allocation, closure, map, gc, torture | 2 |
| l05_short_goroutine_arena | P2 |  | L05, goroutine-local, arena, short-lived-goroutine, ownership-handoff, liveness, gc, torture | 2 |
| l05_skiplist_big_values | P2 |  | L05, skiplist, concurrency, large-object, gc, torture | 1 |
| l05_slice_buckets_gc | P1 |  | L05, slice-buckets, segmented-slice, sub-slice, liveness, gc | 2 |
| l05_stream_large_value_pooled | P1 |  | L05, sync.Pool, large-value, streaming, crc32, gc | 1 |
| l05_string_intern_table | P1 |  | L05, string-intern, shared-backing, dedupe, map, gc | 3 |
| l05_struct_mixed_pointer_fields | P1 |  | L05, struct-layout, mixed-pointers, pointer-bitmap, zeroing, gc | 3 |
| l05_struct_ptr_slice_copy | P1 |  | L05, slice-of-struct, pointer-fields, append-growth, copy, gc | 1 |
| l05_value_delete_rewrite | P1 |  | L05, large-value, delete, rewrite, liveness, gc | 1 |
| l05_varisize_byte_pool | P1 |  | L05, sync.Pool, variable-size, byte-buffer, reuse, gc | 2 |

## L06_reflect_iface — L6 反射与接口（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l06_boxed_struct_pointers | P0 |  | L06, interface-boxing, struct-value, pointer-fields, GC | 2 |
| l06_container_convert_collision | P2 |  | L06, container-conversion, collision, map-key, atomicity, NaN, nil-container, pointer-elements, GC, torture | 4 |
| l06_deepequal_map_semantics | P2 | reflect | L06, reflect, DeepEqual, map, NaN, func-value, interface-key, cycle, GC, torture | 3 |
| l06_deepequal_ptr_graph | P2 | reflect | L06, reflect, DeepEqual, pointer-graph, cycle, func, NaN, GC, torture | 3 |
| l06_empty_iface_containers | P2 |  | L06, empty-interface, nested-containers, type-switch, GC, torture | 2 |
| l06_error_assert_matrix | P1 | reflect | L06, error, errors.As, errors.Is, errors.Join, unwrap-forest, typed-nil, GC | 3 |
| l06_error_custom_as | P1 | reflect | L06, reflect, error, errors.As, custom-As, multi-wrap, Join, forest, typed-nil, panic, GC | 4 |
| l06_error_join_traversal | P1 |  | L06, error, errors.Is, errors.As, errors.Join, unwrap-forest, traversal-order, custom-As, interface-target, GC | 4 |
| l06_error_wrap_chain | P1 |  | L06, error-chain, errors.Is, errors.As, errors.Join, GC | 2 |
| l06_iface_convert_matrix | P2 | reflect | L06, interface-conversion, type-assertion, Implements, ConvertibleTo, GC, torture | 4 |
| l06_iface_embed_diamond | P1 | reflect | L06, interface-embedding, diamond, method-set, Implements, AssignableTo, stdlib-io, nil-embedded-interface, GC | 4 |
| l06_iface_implements_runtime | P1 | reflect | L06, reflect, Implements, AssignableTo, ConvertibleTo, io-interfaces, GC | 3 |
| l06_iface_interp_eval | P2 | reflect | L06, reflect, interface-dispatch, interpreter, type-switch, reflect-call, interface-assertion, closure, GC, torture | 4 |
| l06_iface_ptr_holder | P0 | reflect | L06, interface, pointer-receiver, interface-data, pointer-to-interface, typed-nil, GC | 3 |
| l06_iface_reflect_set_barrier | P0 | reflect | L06, reflect, interface-field, Set, write-barrier, dynamic-type, GC | 3 |
| l06_iface_vm_stack_dispatch | P2 | reflect | L06, interface, interpreter, stack-machine, type-switch, embedded-interface, host-callback, GC, torture | 4 |
| l06_interface_embedding_chain | P1 |  | L06, interface-embedding, wrapper-chain, method-set, itab, GC | 2 |
| l06_make_map_nan_interface_keys | P1 | reflect | L06, reflect, MakeMap, MakeSlice, NaN-key, interface-key, typed-nil, append, GC | 4 |
| l06_makeslice_makemap_bounds | P2 | reflect | L06, reflect, MakeSlice, MakeMap, SetLen, SetCap, append-aliasing, nil-map, panic-boundary, GC, torture | 4 |
| l06_method_expr_iface_bridge | P1 | reflect | L06, method-expression, method-value, interface, typed-nil, embedding, reflect, GC | 4 |
| l06_method_expr_reflect | P1 | reflect | L06, reflect, method-expression, Method.Func, Value.Method, method-set, promotion, unexported-method, GC | 4 |
| l06_method_value_iface | P1 |  | L06, method-value, method-expression, interface-dispatch, method-set, GC | 2 |
| l06_methodset_value_ptr | P0 | reflect | L06, method-set, value-receiver, pointer-receiver, method-value, embedding, GC | 3 |
| l06_reflect_array_ops | P1 | reflect | L06, reflect, array, ArrayOf, index, addressability, reflect-copy, GC | 4 |
| l06_reflect_chan_blocked_pipeline | P2 | reflect | L06, reflect, chan, blocked-goroutine, MakeChan, func-value, MakeFunc, Select, GC, torture | 4 |
| l06_reflect_chan_func | P1 | reflect | L06, reflect, chan, select, MakeFunc, variadic, closure, GC | 4 |
| l06_reflect_container_convert | P2 | reflect | L06, reflect, Convert, container-conversion, map-rebuild, key-conversion, nil-container, error-path, GC, torture | 4 |
| l06_reflect_deep_copy | P2 | reflect | L06, reflect, deep-copy, shallow-copy, pointer-graph, GC, torture | 2 |
| l06_reflect_directional_chan | P1 | reflect | L06, reflect, ChanOf, chan-direction, FuncOf, MakeFunc, select-direction, nil-func, GC | 4 |
| l06_reflect_embedded_fields | P1 | reflect | L06, reflect, embedded-field, promotion, shadowing, FieldByIndex, embedded-interface, GC | 4 |
| l06_reflect_grow_slice_map | P2 | reflect | L06, reflect, slice-growth, map-growth, Append, SetMapIndex, GC, torture | 3 |
| l06_reflect_makefunc_callback | P2 | reflect | L06, reflect, MakeFunc, callback, closure, variadic, sort, panic, GC, torture | 3 |
| l06_reflect_new_set_struct | P1 | reflect | L06, reflect, reflect.New, Set, MakeSlice, MakeMap, struct-copy, GC | 3 |
| l06_reflect_select_matrix | P2 | reflect | L06, reflect, Select, nil-channel, closed-channel, default, chan-any, GC, torture | 3 |
| l06_reflect_slice_map | P1 | reflect | L06, reflect, slice, map, pointer-elements, GC | 2 |
| l06_reflect_struct_fields | P1 | reflect | L06, reflect, struct-field, pointer-field, Set, GC | 2 |
| l06_reflect_type_convert | P1 | reflect | L06, reflect, Convert, CanConvert, named-type, slice-to-array, interface-conversion, panic, GC | 3 |
| l06_reflect_unexported_iface | P1 | reflect | L06, reflect, unexported-field, embedded-struct, interface-field, GC | 3 |
| l06_reflect_variadic_methods | P1 | reflect | L06, reflect, variadic, Call, CallSlice, method-expression, method-set, promoted-method, GC | 3 |
| l06_reflect_visible_fields | P1 | reflect | L06, reflect, VisibleFields, embedded-field, shadowing, ambiguity, FieldByIndex, GC | 3 |
| l06_struct_tag_grammar | P1 | reflect | L06, reflect, struct-tag, StructTag, Lookup, tag-options, escaped-quote, embedded-field, unexported-field, GC | 5 |
| l06_struct_tag_pathological_parse | P1 | reflect | L06, reflect, struct-tag, StructTag, Lookup, malformed-tag, escaping, tag-options, StructOf, GC | 4 |
| l06_type_assert_loop | P0 |  | L06, type-assertion, comma-ok, nil-interface, panics, GC | 2 |
| l06_type_assert_matrix | P0 | reflect | L06, type-assertion, interface, method-set, unexported-method, typed-nil, panic, GC | 4 |
| l06_type_identity_composite_deep | P2 | reflect | L06, reflect, type-identity, composite-type, named-type, interface-identity, StructOf, FuncOf, type-map-key, GC, torture | 4 |
| l06_type_identity_matrix | P0 | reflect | L06, reflect, type-identity, TypeFor, named-type, alias, StructOf, interface-identity, Comparable, GC | 4 |
| l06_type_switch_many | P1 |  | L06, type-switch, interface-boxing, itab, GC | 2 |
| l06_typed_nil_empty_iface | P0 | reflect | L06, typed-nil, empty-interface, reflect, DeepEqual, map-key, panic, GC | 3 |
| l06_visiblefields_materialize | P1 | reflect | L06, reflect, VisibleFields, FieldByIndexErr, embedded-pointer, nil-chain, tag-driven-bind, materialize, GC | 4 |
| l06_visiblefields_self_embed | P1 | reflect | L06, reflect, VisibleFields, self-embedding, promotion, shadowing, ambiguity, FieldByIndexErr, embedded-interface, GC | 4 |

## L07_unsafe_zerocopy — L7 unsafe 与零拷贝（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l07_align_stride_classes | P1 |  | L07, unsafe, alignment, padding, struct-layout, interface-field | 3 |
| l07_alignment_probe_dispatch | P0 |  | L07, unsafe, alignment-probe, copy-dispatch, typed-load, guard-byte, GC | 3 |
| l07_alignment_runtime_probe | P0 |  | L07, unsafe, alignment, Alignof, Offsetof, align-up | 3 |
| l07_array_elem_gep_arith | P1 |  | L07, unsafe, pointer-arithmetic, GEP, arrays, interior-pointer | 2 |
| l07_array_struct_fields_unsafe | P1 |  | L07, unsafe, struct-array, nested-field, array-field, write-barrier | 3 |
| l07_atomic_list_stack | P2 |  | L07, atomic, lock-free-list, CAS, liveness, write-barrier | 3 |
| l07_atomic_pointer_queue | P2 |  | L07, atomic, lock-free-queue, michael-scott, GC, concurrent | 3 |
| l07_atomic_pointer_swap_chain | P2 |  | L07, atomic, pointer-swap, CAS, concurrent, write-barrier | 3 |
| l07_atomic_queue_ring_wrap | P2 |  | L07, atomic, ring-queue, spsc, slot-reuse, wrap-around, GC | 3 |
| l07_binary_tlv_zerocopy | P2 |  | L07, zero-copy, binary-parser, tlv, nested, read-only, liveness | 4 |
| l07_byte_alias_overlap | P0 |  | L07, unsafe, alias-detection, byte-range, append-realloc, GC | 3 |
| l07_byte_pool_alias_copy | P1 |  | L07, unsafe, byte-pool, alias, copy-semantics | 2 |
| l07_byte_to_string_lifetime | P1 |  | L07, unsafe, string, lifetime, alias-vs-copy | 3 |
| l07_field_view_layout_prefix | P1 |  | L07, unsafe, struct-field, prefix-layout, Offsetof, write-barrier | 3 |
| l07_generic_ring_multiwrap | P2 |  | L07, unsafe, generics, ring-buffer, multi-wrap, slot-zeroing, GC | 3 |
| l07_generic_unsafe_ring | P1 |  | L07, unsafe, generics, ring-buffer, Sizeof, GC | 3 |
| l07_generic_unsafe_views | P0 |  | L07, unsafe, generics, slice-header, unsafe.Slice, Sizeof | 3 |
| l07_global_pointer_table | P1 |  | L07, unsafe, global-root, pointer-table, map, slice, GC | 3 |
| l07_header_zero_copy_parse | P2 |  | L07, unsafe, header-parser, zero-copy, slice-offset | 2 |
| l07_interior_pointer_liveness | P0 |  | L07, unsafe, interior-pointer, slice-view, liveness | 2 |
| l07_json_token_byte_alias | P1 |  | L07, unsafe, decoder, byte-alias, json-token, escaped-copy | 3 |
| l07_json_token_truncation_alias | P1 |  | L07, unsafe, json-token, truncation-boundary, byte-alias, escaped-copy, GC | 3 |
| l07_largebuf_dual_views | P2 |  | L07, unsafe, large-buffer, slice-header, string-header, liveness | 3 |
| l07_largepayload_segmented_zerocopy | P2 |  | L07, unsafe, large-payload, zero-copy, string-header, liveness | 3 |
| l07_layout_prefix_cross_type | P1 |  | L07, unsafe, struct-layout, prefix-compat, cross-type-cast, write-barrier, GC | 3 |
| l07_mmap_window_readonly | P2 |  | L07, unsafe, mmap-style, pages, read-only, liveness | 3 |
| l07_pointer_array_slots | P2 |  | L07, unsafe, pointer-array, atomic, pointer-arithmetic, nested-pointer | 2 |
| l07_pointer_slot_arena | P1 |  | L07, unsafe, pointer-slots, arena, reuse, generation, write-barrier | 3 |
| l07_pointer_slot_handles | P1 |  | L07, unsafe, pointer-slots, stable-handle, compaction, write-barrier | 3 |
| l07_query_zerocopy_parse_ro | P2 |  | L07, zero-copy, query-parser, percent-decode, input-immutable | 3 |
| l07_readonly_string_header_view | P0 |  | L07, unsafe, string-header, read-only, KeepAlive, FNV | 3 |
| l07_readonly_window_alias_scan | P2 |  | L07, unsafe, read-only-window, subviews, page-boundary, high-cardinality, GC | 3 |
| l07_segment_record_span_parse | P2 |  | L07, unsafe, segmented, cross-boundary, zero-copy, string-header, liveness | 3 |
| l07_slab_reuse_alias_regions | P2 |  | L07, unsafe, buffer-reuse, slab, alias, snapshot | 3 |
| l07_slice_header_from_heap | P0 |  | L07, unsafe, unsafe.Slice, slice-cap, append, write-barrier | 2 |
| l07_slice_header_lencap_ops | P0 |  | L07, unsafe, slice-header, len-cap, three-index, reslice | 3 |
| l07_slice_header_overlap_exchange | P1 |  | L07, unsafe, slice-header, overlapping-views, cap-append, pointer-swap, GC | 3 |
| l07_slice_header_pointer_math | P0 |  | L07, unsafe, pointer-arithmetic, slice-view, offset | 1 |
| l07_slice_header_swap_transfer | P1 |  | L07, unsafe, slice-header, swap, append-realloc, GC | 3 |
| l07_string_bytes_boundary_keepalive | P1 |  | L07, string, bytes, lifetime, KeepAlive, alias-vs-copy | 3 |
| l07_string_bytes_zero_copy | P0 |  | L07, unsafe, string, bytes, alias | 1 |
| l07_struct_array_inplace_move | P1 |  | L07, unsafe, struct-array, in-place-move, pointer-arithmetic, write-barrier, GC | 3 |
| l07_struct_array_pointer_permute | P1 |  | L07, unsafe, pointer-arithmetic, struct-array, permutation, GC | 3 |
| l07_struct_array_uintptr_walk | P1 |  | L07, unsafe, pointer-arithmetic, uintptr, struct-array, field-pointer | 3 |
| l07_struct_padding_alignment | P0 |  | L07, unsafe, struct-layout, padding, alignment, Offsetof | 3 |
| l07_substring_header_ro | P1 |  | L07, unsafe, string-header, substring, read-only, liveness | 3 |
| l07_unaligned_packed_stream | P0 |  | L07, unsafe, unaligned, packed-struct, little-endian, interior-pointer, GC | 3 |
| l07_unaligned_scalar_load | P0 |  | L07, unsafe, unaligned, scalar-load, little-endian, interior-pointer | 3 |
| l07_unsafe_global_atomic | P1 |  | L07, unsafe, atomic, global, liveness | 2 |
| l07_unsafe_struct_field_set | P1 |  | L07, unsafe, struct-field, offset, write-barrier, zeroing | 2 |

## L08_asm_simd — L8 汇编与 SIMD（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l08_asm_abi0_amd64 | P1 | asm, amd64 | L08, asm, ABI0, amd64 | 2 |
| l08_asm_abs_diff_u8 | P1 | asm, amd64 | L08, asm, amd64, SSE2, PMAXUB, PMINUB, PSUBUSB, abs-diff | 3 |
| l08_asm_abs_i64 | P1 | asm, amd64 | L08, asm, amd64, abs, neg, cmov, int64, minint | 3 |
| l08_asm_bitops | P1 | asm, amd64 | L08, asm, amd64, popcount, rotate, SWAR | 2 |
| l08_asm_bitrev_bytes | P1 | asm, amd64 | L08, asm, amd64, bit-reverse, Reverse8, table-lookup, in-place | 3 |
| l08_asm_bitrev_u64 | P1 | asm, amd64 | L08, asm, amd64, bit-reverse, SWAR, bswap, Reverse64 | 3 |
| l08_asm_bswap | P1 | asm, amd64 | L08, asm, amd64, bswap, endian, ReverseBytes | 2 |
| l08_asm_byte_mismatch | P1 | asm, amd64 | L08, asm, amd64, SSE2, byte-compare, mismatch | 2 |
| l08_asm_bytes_index | P1 | asm, amd64 | L08, asm, amd64, SSE2, bytes.Index, substring-search, PCMPEQB | 3 |
| l08_asm_checksum | P1 | asm, amd64 | L08, asm, amd64, adler32, checksum, mod | 2 |
| l08_asm_clamp_i64 | P1 | asm, amd64 | L08, asm, amd64, clamp, cmov | 2 |
| l08_asm_clz_ctz_u64 | P1 | asm, amd64 | L08, asm, amd64, clz, ctz, BSR, BSF, zero-boundary | 3 |
| l08_asm_clz_u64 | P1 | asm, amd64 | L08, asm, amd64, clz, BSR, LeadingZeros64 | 3 |
| l08_asm_copy_loop | P1 | asm, amd64 | L08, asm, amd64, SSE2, copy, sentinel | 2 |
| l08_asm_count_bits | P1 | asm, amd64 | L08, asm, amd64, BSF, popcount, bit-count | 2 |
| l08_asm_count_zero | P1 | asm, amd64 | L08, asm, amd64, SSE2, popcount, zero-bytes | 2 |
| l08_asm_crc32_bitwise | P1 | asm, amd64 | L08, asm, amd64, crc32, checksum, bitwise, IEEE | 3 |
| l08_asm_crc32_table | P1 | asm, amd64 | L08, asm, amd64, crc32, table-driven, IEEE, chunked | 3 |
| l08_asm_dot_i16 | P1 | asm, amd64 | L08, asm, amd64, SSE2, dot-product | 2 |
| l08_asm_fill_pattern | P1 | asm, amd64 | L08, asm, amd64, fill-pattern, memset, sentinel | 2 |
| l08_asm_first_nonzero | P1 | asm, amd64 | L08, asm, amd64, SSE2, scan | 2 |
| l08_asm_first_set_bit | P1 | asm, amd64 | L08, asm, amd64, BSF, bitmap, first-set-bit, word-scan | 3 |
| l08_asm_first_set_from | P1 | asm, amd64 | L08, asm, amd64, BSF, bitmap, first-set, range-scan | 3 |
| l08_asm_hash_loop | P1 | asm, amd64 | L08, asm, loop, byte-slice, FNV | 1 |
| l08_asm_hex_encode | P1 | asm, amd64 | L08, asm, amd64, hex, nibble, encoding | 3 |
| l08_asm_hex_encode_upper | P1 | asm, amd64 | L08, asm, amd64, hex, uppercase, nibble, encoding | 3 |
| l08_asm_index_any2 | P1 | asm, amd64 | L08, asm, amd64, SSE2, PCMPEQB, two-delimiters, byte-index | 3 |
| l08_asm_last_index_sub | P1 | asm, amd64 | L08, asm, amd64, SSE2, LastIndex, backward-scan, substring | 3 |
| l08_asm_maxmin | P1 | asm, amd64 | L08, asm, amd64, max-min, branch | 2 |
| l08_asm_memchr | P1 | asm, amd64 | L08, asm, amd64, SSE2, memchr, byte-scan | 2 |
| l08_asm_memeq | P1 | asm, amd64 | L08, asm, amd64, SSE2, memequal, bytes-equal | 2 |
| l08_asm_memrchr | P1 | asm, amd64 | L08, asm, amd64, SSE2, memrchr, LastIndexByte, backward-scan | 3 |
| l08_asm_memset_fill | P1 | asm, amd64 | L08, asm, amd64, memset, fill, sentinel | 2 |
| l08_asm_minmax_u8 | P1 | asm, amd64 | L08, asm, amd64, SSE2, PMINUB, PMAXUB, min-max | 2 |
| l08_asm_mul128 | P1 | asm, amd64 | L08, asm, amd64, mul, 128-bit | 3 |
| l08_asm_overlap_copy | P1 | asm, amd64 | L08, asm, amd64, memmove, overlap, copy | 2 |
| l08_asm_ptr_array | P1 | asm, amd64 | L08, asm, amd64, pointer-array, struct-field, GC | 2 |
| l08_asm_ptr_chase | P1 | asm, amd64 | L08, asm, amd64, linked-list, pointer-chase, GC | 2 |
| l08_asm_reverse_bytes | P1 | asm, amd64 | L08, asm, amd64, reverse, in-place | 2 |
| l08_asm_sat_add_i64 | P1 | asm, amd64 | L08, asm, amd64, saturating-add, overflow, int64, maxmin | 3 |
| l08_asm_sat_add_u8 | P1 | asm, amd64 | L08, asm, amd64, SSE2, PADDUSB, PSUBUSB, saturating, uint8 | 3 |
| l08_asm_shift_rot | P1 | asm, amd64 | L08, asm, amd64, shift, rotate, masking | 2 |
| l08_asm_slice_sum | P1 | asm, amd64 | L08, asm, amd64, slice, sum, loop-unroll | 2 |
| l08_asm_strcmp_lex | P1 | asm, amd64 | L08, asm, amd64, strcmp, byte-compare | 2 |
| l08_asm_struct_array_sum | P1 | asm, amd64 | L08, asm, amd64, struct-array, field-offset, GC | 2 |
| l08_asm_struct_fill | P1 | asm, amd64 | L08, asm, amd64, struct, pointer-field, GC | 3 |
| l08_asm_sum_bytes | P1 | asm, amd64 | L08, asm, amd64, SSE2, PSADBW, sum-bytes | 2 |
| l08_asm_sum_u32 | P1 | asm, amd64 | L08, asm, amd64, uint32, sum, unroll | 2 |
| l08_asm_xor_bytes | P1 | asm, amd64 | L08, asm, amd64, SSE2, PXOR, xor, in-place, sentinel | 3 |
| l08_asm_xor_fold_rol | P1 | asm, amd64 | L08, asm, amd64, xor, rotate, fold, checksum | 3 |

## L09_cgo — L9 cgo（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l09_cgo_alloc_failure | P1 | cgo | L09, cgo, posix_memalign, EINVAL, ENOMEM, calloc-overflow, realloc, C-heap, GC | 3 |
| l09_cgo_bitfield_header | P1 | cgo | L09, cgo, bitfield, signed-bitfield, masking, layout, GC | 3 |
| l09_cgo_c_sort | P1 | cgo | L09, cgo, c-sort, insertion-sort, merge-sort, stability, struct-array | 3 |
| l09_cgo_callback | P1 | cgo | L09, cgo, callback, export, function-pointer, GC | 2 |
| l09_cgo_const_string | P1 | cgo | L09, cgo, const-char*, GoString, copy | 2 |
| l09_cgo_cstring | P1 | cgo | L09, cgo, CString, GoString, CBytes, NUL | 2 |
| l09_cgo_errno | P1 | cgo | L09, cgo, errno, syscall, ENOENT | 2 |
| l09_cgo_errno_chain | P1 | cgo | L09, cgo, errno, ENOENT, EISDIR, EBADF, syscall, errors.Is, GC | 4 |
| l09_cgo_errno_edge | P1 | cgo | L09, cgo, errno, ERANGE, pthread, thread-local, syscall | 3 |
| l09_cgo_errno_stdio | P1 | cgo | L09, cgo, errno, stdio, FILE*, ENOENT, EISDIR, ERANGE, strtol, GC | 4 |
| l09_cgo_fenv_flags | P1 | cgo | L09, cgo, fenv, exceptions, FE_INVALID, FE_OVERFLOW, FE_UNDERFLOW, rounding, LockOSThread, GC | 4 |
| l09_cgo_fmemopen_modes | P1 | cgo | L09, cgo, fmemopen, FILE*, bounded-buffer, append, truncation, EOF, GC | 3 |
| l09_cgo_fnptr_list | P1 | cgo | L09, cgo, function-pointer, linked-list, struct, dispatch, recursion, GC | 4 |
| l09_cgo_fnptr_table | P1 | cgo | L09, cgo, function-pointer, dispatch | 2 |
| l09_cgo_go_slice_to_c | P1 | cgo | L09, cgo, go-slice, sentinel, pointer-rules | 2 |
| l09_cgo_libm_math | P1 | cgo | L09, cgo, libm, float, math | 2 |
| l09_cgo_malloc_align | P1 | cgo | L09, cgo, posix_memalign, alignment, malloc, EINVAL, GC | 3 |
| l09_cgo_malloc_and_slice | P1 | cgo | L09, cgo, malloc, Go-pointer-to-C | 1 |
| l09_cgo_malloc_free_churn | P1 | cgo | L09, cgo, malloc, free, realloc, leak-balance, GC | 3 |
| l09_cgo_malloc_gc_rounds | P1 | cgo | L09, cgo, malloc, calloc, GC-rounds, unsafe.Pointer | 2 |
| l09_cgo_math_precision | P1 | cgo | L09, cgo, libm, fenv, rounding, long-double, IEEE-754, GC | 3 |
| l09_cgo_math_special | P1 | cgo | L09, cgo, libm, special-values, rounding, NaN, frexp | 3 |
| l09_cgo_memcpy | P1 | cgo | L09, cgo, memcpy, sentinel, struct-array, malloc, GC | 3 |
| l09_cgo_memmove_overlap | P1 | cgo | L09, cgo, memmove, overlap, sentinel | 2 |
| l09_cgo_memstream | P1 | cgo | L09, cgo, FILE*, open_memstream, fmemopen, fgets, EOF, GC | 3 |
| l09_cgo_nested_struct | P1 | cgo | L09, cgo, nested-struct, layout | 2 |
| l09_cgo_posix_regex | P1 | cgo | L09, cgo, regex, POSIX-ERE, regcomp, captures, glibc, GC | 3 |
| l09_cgo_pure_calls | P1 | cgo | L09, cgo, C-call, pointer-liveness | 1 |
| l09_cgo_qsort_cb | P1 | cgo | L09, cgo, qsort, callback, export, GC | 3 |
| l09_cgo_qsort_r_aligned | P1 | cgo | L09, cgo, qsort_r, posix_memalign, struct-array, alignment, context, GC | 4 |
| l09_cgo_qsort_struct | P1 | cgo | L09, cgo, qsort, struct-array, export-callback, bsearch, GC | 3 |
| l09_cgo_realloc_array | P1 | cgo | L09, cgo, realloc, array-growth, alignment, C-heap, GC | 3 |
| l09_cgo_realloc_structs | P1 | cgo | L09, cgo, realloc, struct-array, prefix-preservation, FNV, alignment, C-heap, GC | 4 |
| l09_cgo_regex_startend | P1 | cgo | L09, cgo, regex, REG_STARTEND, embedded-NUL, REG_NOTBOL, REG_NOTEOL, backreference, GC | 3 |
| l09_cgo_return_ptr_len | P1 | cgo | L09, cgo, ptr-len, unsafe.Slice, malloc, NULL | 2 |
| l09_cgo_snprintf_positional | P1 | cgo | L09, cgo, snprintf, varargs, positional-args, dynamic-width, %n, truncation, GC | 3 |
| l09_cgo_snprintf_varargs | P1 | cgo | L09, cgo, snprintf, varargs, formatting, truncation, C-heap, GC | 3 |
| l09_cgo_stateful_cb | P1 | cgo | L09, cgo, callback, stateful, handle, export, GC | 3 |
| l09_cgo_string_array | P1 | cgo | L09, cgo, string-array, char**, GoString, NULL-terminated, GC | 3 |
| l09_cgo_strsep_fields | P1 | cgo | L09, cgo, strsep, strtok_r, empty-fields, saveptr, in-place, CString, GC | 3 |
| l09_cgo_strtok_r | P1 | cgo | L09, cgo, strtok_r, tokenize, saveptr, in-place, CString, GC | 3 |
| l09_cgo_struct | P1 | cgo | L09, cgo, struct, sizeof, offsetof, layout | 2 |
| l09_cgo_struct_array | P1 | cgo | L09, cgo, struct-array, unsafe.Slice, GC | 2 |
| l09_cgo_struct_fnptr | P1 | cgo | L09, cgo, struct, function-pointer, vtable, layout, dispatch, GC | 3 |
| l09_cgo_struct_ptr_array | P1 | cgo | L09, cgo, struct-pointer-array, double-pointer, unsafe.Slice, GC | 3 |
| l09_cgo_union64 | P1 | cgo | L09, cgo, union, little-endian, punning | 2 |
| l09_cgo_union_bitfield | P1 | cgo | L09, cgo, union, bitfield, layout, little-endian | 2 |
| l09_cgo_union_tagged | P1 | cgo | L09, cgo, union, tagged-union, raw-alias, offsetof, GC | 3 |
| l09_cgo_userdata_cb | P1 | cgo | L09, cgo, userdata, callback, export, GC | 3 |
| l09_cgo_write_go_array | P1 | cgo | L09, cgo, go-array, length-return, LEB128, sentinel | 2 |

## L10_codegen — L10 代码生成（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l10_clone_pool_gc | P2 |  | L10, L05, L18, clone, sync.Pool, gc-lifecycle, alias, retention, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_codec_registry | P2 |  | L10, L06, codec-registry, alias, negotiation, q-value, snapshot, freeze, gzip, gc, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_codec_version_forward | P2 |  | L10, L12, codec-registry, versioning, forward-compat, unknown-fields, raw-bytes, atomic-decode, group, gc, UAF_SEED, TORTURE_ROUNDS | 5 |
| l10_codegen_parse_check | P1 | goast | L10, L16, goast, text/template, go-parser, go-types, AST, UAF_SEED | 3 |
| l10_deepcopy_cycle | P1 |  | L10, deepcopy, cycle, aliasing, identity-map, gc, UAF_SEED | 3 |
| l10_deepcopy_generated | P1 |  | L10, deepcopy, nested-struct, slice, map, aliasing, gc, UAF_SEED | 2 |
| l10_deepcopy_oneof_map | P1 |  | L10, deepcopy, oneof, map-bytes, reuse-destination, nil-semantics, gc, UAF_SEED | 5 |
| l10_default_fill | P1 | goast | L10, L16, goast, defaults, presence, recursive-fill, idempotent, gc, UAF_SEED | 4 |
| l10_defaults_alias_copy | P1 |  | L10, L06, defaults, presence, copy-on-read, copy-on-set, frozen-table, merge, reset, gc, UAF_SEED | 5 |
| l10_defaults_decode | P1 |  | L10, L16, defaults, decode-time, presence, bytes-escaping, merge, reset, table-build, gc, UAF_SEED | 4 |
| l10_descriptor_dyn_json | P1 |  | L10, L06, descriptor, reflect, dynamic-json, presence, map-slice, enum, nested, unknown-field, gc, UAF_SEED | 5 |
| l10_descriptor_reflect | P1 | reflect | L10, L06, descriptor, reflect, struct-tag, dynamic-access, presence, oneof, registry, gc, UAF_SEED | 4 |
| l10_descriptor_table | P1 |  | L10, L12, descriptor-table, flattening, path-lookup, offset-table, validation, gc, UAF_SEED | 3 |
| l10_dispatch_switch | P2 |  | L10, L11, dispatch-switch, 1024-opcodes, default-branch, jump-table, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_dispatch_table | P2 |  | L10, L11, dispatch-table, func-pointer, closure-capture, holes, indirect-call, gc, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_enum_alias_wire | P1 |  | L10, L06, enum, alias, unknown-enum, wire, json, int32-range, gc, UAF_SEED | 5 |
| l10_enum_map | P1 |  | L10, L13, enum-map, alias, canonical, copy-on-write, set-algebra, map-growth, gc, UAF_SEED | 4 |
| l10_enum_mapping | P0 |  | L10, L06, enum, mapping-table, alias, json, text-marshal, gc, UAF_SEED | 4 |
| l10_enum_stringer | P0 |  | L10, L11, enum, stringer, index-table, prefix-trim, text-marshal, gc, UAF_SEED | 4 |
| l10_enum_switch | P0 |  | L10, L11, enum, stringer, switch-table, 500-values, gc, UAF_SEED | 2 |
| l10_field_validation | P1 |  | L10, validation, required, range, enum, repeated, map, deterministic-errors, gc, UAF_SEED | 3 |
| l10_fixed_size_codec | P2 |  | L10, L13, fixed-size, fixed32, fixed64, endian, bitfield, crc, truncation, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_generated_getters | P1 |  | L10, L06, opaque-accessors, presence, nil-receiver, reset, gc, UAF_SEED | 2 |
| l10_generated_validator | P1 |  | L10, L16, validator, rule-plan, spec-validation, violation-order, nested, oneof, cross-field, depth-limit, cycle, gc, UAF_SEED | 4 |
| l10_init_registry | P1 |  | L10, L12, init, registry, duplicate, freeze, sync.Once, gc | 3 |
| l10_large_const_table | P2 |  | L10, L11, const-table, compile-time-folding, binary-search, GC, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_large_methodset | P2 |  | L10, L06, method-set, interface-dispatch, method-value, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_map_slice_accessors | P1 |  | L10, L13, container-accessors, map, slice, nil-vs-empty, aliasing, gc, UAF_SEED | 5 |
| l10_map_slice_decoder | P2 |  | L10, L13, decoder, packed-repeated, zigzag, map-entry, duplicate-key, nil-vs-empty, merge, gc, UAF_SEED, TORTURE_ROUNDS | 6 |
| l10_marshal_buffer | P1 |  | L10, marshal, append-buffer, packed-repeated, size, gc, UAF_SEED | 3 |
| l10_oneof_codec | P0 |  | L10, L06, oneof, codec, json, branch-table, typed-nil, null, duplicate-key, base64, gc, UAF_SEED | 5 |
| l10_oneof_roundtrip | P0 |  | L10, L06, oneof, marshal, roundtrip, typed-nil, last-one-wins, unknown-field, gc, UAF_SEED | 3 |
| l10_oneof_union | P0 |  | L10, L06, oneof, type-switch, typed-nil, last-one-wins, gc, UAF_SEED | 2 |
| l10_oneof_unknown_wire | P1 |  | L10, L06, oneof, unknown-fields, wire, typed-nil, last-one-wins, merge, clear, group, gc, UAF_SEED | 5 |
| l10_optional_getters | P1 |  | L10, L06, optional, getter, presence, default-table, nil-receiver, method-value, gc, UAF_SEED | 3 |
| l10_proto_struct_switch | P1 |  | L10, generated-struct, field-switch, protowire, zigzag, unknown-field, gc, UAF_SEED | 3 |
| l10_registry_dup_detect | P2 |  | L10, L12, descriptor-registry, duplicate-detect, atomic-commit, conflict-sort, gc, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_repeated_merge_dirty | P2 |  | L10, L13, merge, dirty-destination, packed-repeated, map-entry, capacity-reuse, atomic-failure, gc, UAF_SEED, TORTURE_ROUNDS | 5 |
| l10_resolver_table | P1 |  | L10, L12, resolver, scheme-registry, address-map, endpoint-map, alias-key, state-machine, gc, UAF_SEED | 5 |
| l10_resolver_target_boundary | P1 |  | L10, L12, resolver, target-parsing, IPv6, percent-escape, weighted-endpoints, dedup, generation-guard, gc, UAF_SEED | 5 |
| l10_service_codec_stub | P1 |  | L10, L06, rpc-stub, codec, codec-negotiation, json, golden-bytes, gc, UAF_SEED | 3 |
| l10_service_desc_table | P1 |  | L10, L12, service-desc, method-table, duplicate-detect, atomic-register, closure, gc, UAF_SEED | 3 |
| l10_service_stub | P2 |  | L10, L06, rpc-stub, codec-registry, error-path, max-size, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_string_intern_refs | P2 |  | L10, L13, L05, string-table, interning, refcount, collision, freeze, tombstone, gc, UAF_SEED, TORTURE_ROUNDS | 5 |
| l10_string_table | P0 |  | L10, L13, string-table, interning, hashing, growth, freeze, snapshot, json-name, gc, UAF_SEED | 5 |
| l10_template_format_ast | P1 | goast | L10, L16, goast, text-template, go-format, go-parser, AST, directive, GC, UAF_SEED | 3 |
| l10_thrift_binary | P2 |  | L10, L13, thrift-binary, skip, nested-struct, length-table, GC, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_unknown_fields | P2 |  | L10, L13, unknown-fields, raw-bytes, group, wire-type, merge, reset, gc, UAF_SEED, TORTURE_ROUNDS | 5 |
| l10_unknown_json_preserve | P2 |  | L10, L06, json, unknown-fields, canonicalization, presence, map, nested, gc, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_validator_descriptor_reflect | P1 | goast | L10, L16, goast, go-parser, reflect, validator, required, utf8, rune-length, enum, oneof, nested, cycle, depth, map-slice, gc, UAF_SEED | 5 |

## L11_compiler_opts — L11 编译期优化（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l11_bce_cap_hoist | P2 |  | bounds-check-elimination, slice-cap, three-index-slice, reslice, loop-hoisting, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_bce_loop_rebind | P1 |  | bounds-check-elimination, loop-hoisting, reslice, slice-header-rebind, panic-recover, escape-analysis, gc, UAF_SEED | 3 |
| l11_bce_multi_index | P2 |  | bounds-check-elimination, multi-index, adjacent-index, reverse-index, slice-copy, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_bce_multidim | P2 |  | bounds-check-elimination, multi-dimensional, matrix, jagged, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_bounds_check | P1 |  | bounds-check-elimination, panic-recover, slice-bounds, loop-hoisting, gc, UAF_SEED | 3 |
| l11_closure_map_escape | P1 |  | escape-analysis, closure, map, loop-variable, gc, UAF_SEED | 3 |
| l11_const_divmod | P0 |  | const-fold, division, modulo, magic-number, signed-truncation, wrapping, panic-recover, gc, UAF_SEED | 3 |
| l11_const_float | P0 |  | const-fold, float, float32, complex, conversion, rounding, negative-zero, gc | 3 |
| l11_const_float_edges | P0 |  | const-fold, float, float32, ties-to-even, subnormal, NaN, infinity, negative-zero, map-key, gc, UAF_SEED | 4 |
| l11_const_fold | P0 |  | const-fold, bitops, shifts, wrapping, constant-string, constant-array, gc | 3 |
| l11_const_shift | P0 |  | const-fold, shifts, big-int, arithmetic-shift, narrow-integer, wrapping, panic-recover, gc, UAF_SEED | 3 |
| l11_copy_elision | P1 |  | struct-copy, pointer-field, aliasing, slice-copy, array-copy, gc | 2 |
| l11_dce_side_effects | P0 |  | dce, side-effect, dead-branch, panic-recover, allocation, gc | 2 |
| l11_dead_store | P1 |  | dead-store-elimination, dce, aliasing, side-effect, gc, UAF_SEED | 2 |
| l11_dead_store_branch | P0 |  | dead-store-elimination, dce, branch, switch, phi, loop, aliasing, side-effect, panic-recover, gc, UAF_SEED | 3 |
| l11_dead_store_swap | P1 |  | dead-store-elimination, parallel-assignment, swap, aliasing, side-effect, gc, UAF_SEED | 3 |
| l11_defer_panic_gc | P1 |  | defer, panic-unwind, recover, named-result, typed-nil, defer-order, gc, UAF_SEED | 3 |
| l11_defer_stack_gc | P1 |  | defer, recursion, stack-growth, panic-unwind, recover, method-value, gc, UAF_SEED | 3 |
| l11_devirt_iface_array | P2 |  | devirtualize, interface-field, interface-array, array-copy, value-copy, embedded-interface, typed-nil, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_devirt_iface_field | P1 |  | devirtualize, interface-field, embedded-interface, interface-dispatch, type-assertion, gc, UAF_SEED | 3 |
| l11_devirtualize | P1 |  | devirtualize, interface-dispatch, itab, method-value, type-switch, typed-nil, gc | 3 |
| l11_escape_analysis | P1 |  | escape-analysis, closure, interface-boxing, heap, gc, UAF_SEED | 3 |
| l11_escape_channel | P2 |  | escape-analysis, channel, goroutine, pipeline, interface-boxing, closure, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_iface_devirt_multi | P1 |  | devirtualize, interface-dispatch, type-switch, method-value, generics, typed-nil, gc, UAF_SEED | 3 |
| l11_iface_escape | P2 |  | escape-analysis, interface, interface-sink, boxing, type-assertion, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_iface_escape_closure | P2 |  | escape-analysis, closure, interface, capture, loop-variable, any, map, typed-nil, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_inline_budget | P1 |  | inline, recursion, inline-budget, noinline, argument-evaluation, stack-growth, gc, UAF_SEED | 3 |
| l11_inline_budget_variadic | P1 |  | inline, inline-budget, variadic, multi-return, noinline, slice-aliasing, escape-analysis, argument-evaluation, gc, UAF_SEED | 3 |
| l11_inline_closure | P1 |  | inline, closure, capture, escape, recursive-closure, noinline, gc, UAF_SEED | 3 |
| l11_inline_equivalence | P0 |  | inline, noinline, go:noinline, pointer-after-gc, alias-in-slice, gc | 2 |
| l11_itab_multi_impl | P1 |  | devirtualize, interface-dispatch, itab, type-switch, method-value, generics, gc, UAF_SEED | 3 |
| l11_method_value_alloc | P2 |  | method-value, receiver-copy, pointer-receiver, method-expression, interface, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_nil_checks | P0 |  | nil-pointer, nil-receiver, typed-nil, nil-map, nil-slice, nil-chan, gc | 2 |
| l11_nil_func_iface | P0 |  | nil-func, nil-check, typed-nil, interface, defer, panic-recover, gc, UAF_SEED | 3 |
| l11_nil_iface_dispatch | P0 |  | nil-interface, typed-nil, nil-receiver, devirtualize, interface-dispatch, embed-interface, nil-func, error-interface, errors-as, gc, UAF_SEED | 3 |
| l11_nil_map_read | P0 |  | nil-map, map-read, comma-ok, delete, clear, typed-nil, panic-recover, gc, UAF_SEED | 3 |
| l11_nilcheck_elim | P0 |  | nil-check-elimination, repeated-deref, pointer-chain, panic-recover, gc, UAF_SEED | 3 |
| l11_open_coded_defer | P0 |  | open-coded-defer, defer-order, recover, named-result, stack-growth, gc, UAF_SEED | 3 |
| l11_panic_deadcode | P0 |  | dce, dead-branch, unreachable-code, panic-recover, divide-by-zero, side-effect, gc, UAF_SEED | 3 |
| l11_recursive_inline | P1 |  | inline, recursion, mutual-recursion, stack-growth, panic-recover, gc, UAF_SEED | 3 |
| l11_slice_cap_bounds | P2 |  | slice-cap, three-index-slice, append, capacity-sentinel, bounds-check, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_slice_copy_overlap | P2 |  | slice-copy, overlapping-copy, memmove, copy-return, string-bytes, shallow-copy, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_slice_copy_selfappend | P1 |  | slice-copy, overlapping-copy, self-append, memmove, append-growth, shallow-copy, write-barrier, gc, UAF_SEED | 3 |
| l11_string_bytes_conv | P1 |  | string-bytes-conversion, copy-semantics, unicode, invalid-utf8, map-key, gc, UAF_SEED | 3 |
| l11_string_compare | P2 |  | string-compare, memequal, lexicographic, sort, map-key, unicode, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_string_concat | P2 |  | string-concat, strings.Builder, byte-append, rune-count, unicode, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l11_string_concat_chain | P1 |  | string-concat, constant-fold, strings.Builder, strings.Join, unicode, gc, UAF_SEED | 3 |
| l11_string_concat_escape | P2 |  | string-concat, strings-builder, escape-analysis, substring-aliasing, invalid-utf8, map-key, interface-boxing, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_switch_opt | P2 |  | switch, jump-table, if-chain, string-switch, dispatch, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_zero_size_struct | P1 |  | zero-size-struct, struct{}, slice, map-set, channel, interface-equality, gc, UAF_SEED | 3 |

## L12_init_link — L12 初始化与链接（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l12_big_global_array | P2 |  | L12, global-array, large-initializer, pointer-fields, write-barrier, gc, TORTURE_ROUNDS | 3 |
| l12_big_init_table | P2 |  | L12, large-init-table, function-values, map, linked-list, payload-pointers, gc, TORTURE_ROUNDS | 3 |
| l12_blank_import_register | P1 |  | L12, blank-identifier, package-init, registry, side-effect, concurrency, gc | 2 |
| l12_blank_import_sideeffect | P1 |  | L12, blank-import, package-init, side-effect, image/png, net/http/pprof, registry, gc | 2 |
| l12_blank_import_transitive | P1 | pkgbuild | L12, module, blank-import, transitive-import, side-effect, linker-retention, package-init, gc | 3 |
| l12_const_init_expr | P0 |  | L12, constants, constant-folding, typed-constants, iota, shifts, default-types, array-length, gc | 4 |
| l12_const_methodset | P0 |  | L12, constants, iota, method-set, value-receiver, pointer-receiver, interface, itab, gc | 3 |
| l12_crossfile_iface_impl | P2 |  | L12, interface, cross-file-methods, method-set, embedding, typed-nil, itab, gc, TORTURE_ROUNDS | 3 |
| l12_crosspkg_const_boundary | P1 | pkgbuild | L12, module, constants, const-chain, cross-package, typed-constants, zero-length-array, int64-boundary, complex, gc | 4 |
| l12_crosspkg_const_chain | P0 | pkgbuild | L12, module, constants, const-chain, cross-package, typed-constants, iota, array-length, constant-folding, gc | 3 |
| l12_crosspkg_init_chain | P1 | pkgbuild | L12, module, cross-package, init-order, linear-chain, diamond-import, partial-order, gc | 3 |
| l12_crosspkg_once_graph | P2 | pkgbuild | L12, module, sync.Once, cross-package, dependency-DAG, lazy-init, concurrency, gc, TORTURE_ROUNDS | 4 |
| l12_dup_register_crosspkg | P1 | pkgbuild | L12, module, registry, duplicate-detection, cross-package-init, order-independent, atomicity, gc | 3 |
| l12_dup_register_detect | P1 |  | L12, duplicate-detection, registry, panic, error, case-insensitive, atomicity, gc | 3 |
| l12_errors_join_nested | P1 |  | L12, init-error, errors.Join, nested-join, multi-w, custom-Is, errors.As, error-tree, pointer-payload, gc | 4 |
| l12_func_init_chain | P0 |  | L12, package-init, var-init, function-call, init-order, side-effect, gc | 3 |
| l12_global_func_cycle | P2 | pkgbuild | L12, module, global-variables, func-values, mutual-recursion, late-binding, method-value, interface-func, gc, TORTURE_ROUNDS | 3 |
| l12_global_func_values | P2 |  | L12, global-variables, func-values, method-value, method-expression, closures, dispatch, gc, TORTURE_ROUNDS | 3 |
| l12_global_once_matrix | P2 |  | L12, sync.Once, lazy-init, panic, concurrency, gc, TORTURE_ROUNDS | 3 |
| l12_global_ptr_tables | P1 |  | L12, global-table, map, slice, pointer-identity, cross-reference, gc, concurrency | 3 |
| l12_global_struct_gc | P1 |  | L12, global-struct, gc, pointers, map, slice, interface, self-reference | 2 |
| l12_init_defer_panic_nil | P0 |  | L12, init, defer, LIFO, panic, panic-nil, recover, nested-panic, cross-file-init, gc | 4 |
| l12_init_defer_recover | P0 |  | L12, init, defer, LIFO, panic, recover, cross-file-init, gc | 3 |
| l12_init_error_path | P1 |  | L12, init-error, recover, fallback, deterministic, multi-file, gc | 2 |
| l12_init_errors_join | P1 |  | L12, init-error, errors.Join, multi-error, recover, error-tree, pointer-payload, gc | 3 |
| l12_init_goroutine_handoff | P1 |  | L12, init, goroutine, channel, happens-before, handoff, pointer-graph, gc | 3 |
| l12_init_handoff_pipeline | P1 |  | L12, init, goroutine, channel, happens-before, handoff, pipeline, pointer-graph, gc | 3 |
| l12_init_panic_nested_subprocess | P1 | pkgbuild | L12, init-error, init-panic, panic-nil, deferred-panic, os-Exit, blank-import, nested-package, subprocess, exit-code, gc | 4 |
| l12_init_panic_subprocess | P1 |  | L12, init-error, init-panic, os.Exit, recover, subprocess, exit-code, gc | 3 |
| l12_init_runs_once | P0 |  | L12, init-order, multi-file, runs-once, gc | 2 |
| l12_iota_consts | P0 |  | L12, iota, typed-constants, untyped-constants, shifts, constant-folding, gc | 2 |
| l12_large_initializer | P2 |  | L12, large-initializer, gcprog, static-data, map, pointers, gc, TORTURE_ROUNDS | 2 |
| l12_module_init_order | P1 | pkgbuild | L12, module, multi-package, package-init, init-order, var-init, cross-package, gc | 3 |
| l12_multi_file_methods | P1 |  | L12, multi-file, methods, interface, embedding, method-set, dispatch, gc | 2 |
| l12_once_dependency_dag | P2 |  | L12, sync.Once, dependency-DAG, lazy-init, concurrency, happens-before, gc, TORTURE_ROUNDS | 3 |
| l12_oncefunc_values_nested | P2 |  | L12, sync.Once, OnceFunc, OnceValue, OnceValues, nested-lazy, panic, concurrency, gc, TORTURE_ROUNDS | 4 |
| l12_register_sequence | P1 |  | L12, registry, registration-order, sequence, stable-sort, tombstone, gc, concurrency | 3 |
| l12_registry_duplicates | P1 |  | L12, registry, duplicate-detection, stable-order, freeze, concurrency, gc | 2 |
| l12_registry_overlay_merge | P2 | pkgbuild | L12, module, registry, cross-package-init, blank-import, priority-merge, field-overlay, tombstone, freeze, deterministic-order, gc, TORTURE_ROUNDS | 3 |
| l12_registry_priority_merge | P1 | pkgbuild | L12, module, registry, cross-package-init, blank-import, priority-merge, deterministic-order, freeze, gc | 3 |
| l12_static_composite_table | P2 |  | L12, static-data, composite-literal, linker-relocation, gcprog, pointer-identity, map, gc, TORTURE_ROUNDS | 3 |
| l12_static_iface_gcprog | P2 |  | L12, static-data, composite-literal, interface-values, linker-relocation, gcprog, pointer-identity, func-value, map-literal, gc, TORTURE_ROUNDS | 4 |
| l12_subpkg_init_module | P1 | pkgbuild | L12, module, subpackage-init, dependency-order, runs-once, second-importer, gc | 3 |
| l12_sync_once_init | P2 |  | L12, sync.Once, lazy-init, concurrency, gc, TORTURE_ROUNDS | 2 |
| l12_tags_init | P1 | pkgbuild | L12, build-tags, GOOS-file-selection, custom-tag, ignore-tag, package-init, gc | 3 |
| l12_tags_init_additive | P1 | pkgbuild | L12, module, build-tags, compound-constraint, additive-files, package-init, GOOS-GOARCH, gc | 3 |
| l12_tags_suffix_matrix | P1 | pkgbuild | L12, module, build-tags, filename-suffix, GOOS-GOARCH, unix-tag, cgo-tag, compound-constraint, package-init, gc | 4 |
| l12_var_dependency_order | P0 |  | L12, package-init, var-dependency-order, cross-file, gc | 2 |
| l12_var_init_call_memo | P2 |  | L12, package-init, var-initializer, function-call, memoization, pointer-identity, closures, gc, TORTURE_ROUNDS | 3 |
| l12_var_init_panic_subprocess | P1 |  | L12, var-init, panic, recover, subprocess, exit-code, init-order, gc | 3 |

## L13_runtime_ds — L13 运行时数据结构（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l13_bigkey_map_hash | P2 |  | L13, map, large-key, fnv1a, hash-index, ownership, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_builder_growth_boundary | P1 |  | L13, strings.Builder, growth, WriteRune, Reset, clone, gc, UAF_SEED | 3 |
| l13_byte_pool_reuse | P2 |  | L13, sync.Pool, byte-buffer, size-class, zeroing, ownership, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_byte_ring_wrap | P0 |  | L13, ring-buffer, wrap, overwrite, snapshot, gc, UAF_SEED | 3 |
| l13_bytes_buffer_alias_grow | P1 |  | L13, bytes.Buffer, bytes-alias, grow, truncate, next, unread, readfrom, writeto, gc, UAF_SEED | 3 |
| l13_bytes_buffer_reset_reuse | P1 |  | L13, bytes.Buffer, reset, reuse, grow, truncate, readfrom, writeto, gc, UAF_SEED | 3 |
| l13_bytes_reader_seek | P0 |  | L13, bytes.Reader, seek, readat, writeto, unread, buffer-retention, gc, UAF_SEED | 3 |
| l13_bytes_string_map_key | P1 |  | L13, bytes-to-string, map-key, ownership, binary-key, growth, gc, UAF_SEED | 3 |
| l13_chunkreader_buffered | P0 |  | L13, chunk-reader, fixed-window, compaction, EOF, gc, UAF_SEED | 1 |
| l13_container_heap_ptr | P1 |  | L13, container/heap, priority-queue, fix, remove, pointer, gc, UAF_SEED | 2 |
| l13_container_ring_rewire | P2 |  | L13, container/ring, link, unlink, move, pointer-value, rewire, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_hashmap_collision_probe | P2 |  | L13, map, hash-collision, separate-chaining, tombstone, rehash, pointer-value, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_iobuf_capacity_semantics | P0 |  | L13, buffer-pool, size-class, capacity, gc, UAF_SEED | 1 |
| l13_line_reader_long_lines | P0 |  | L13, bufio, readline, long-line, buffer-boundary, gc, UAF_SEED | 1 |
| l13_lru_map_list | P2 |  | L13, map, container/list, lru, eviction, move-to-front, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l13_map_clear_reuse | P2 |  | L13, map, clear, reuse, nil-map, nested-map, alias, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l13_map_grow_delete_gc | P0 |  | L13, map, growth, delete, gc | 1 |
| l13_map_iterate_mutate | P0 |  | L13, map, iteration, delete-during-range, insert-during-range, gc | 2 |
| l13_map_of_maps | P1 |  | L13, map-of-maps, nested, alias, nil-map, gc, UAF_SEED | 2 |
| l13_map_ptr_key_identity | P2 |  | L13, map, pointer-key, identity, overwrite, delete, reinsert, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_map_string_slice_ptr_churn | P2 |  | L13, map, slice-of-pointers, churn, truncate, snapshot, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_map_struct_value_gc | P1 |  | L13, map, struct-value, value-copy, slice-sharing, pointer-field, overwrite, delete, gc, UAF_SEED | 3 |
| l13_map_sweep_nested | P1 |  | L13, map, delete-during-range, nested, alias, gc, UAF_SEED | 3 |
| l13_maps_clone_equal | P1 |  | L13, maps, slices, clone, shallow-copy, equal, delete-func, compact, gc, UAF_SEED | 4 |
| l13_maps_copy_deletefunc | P1 |  | L13, maps, copy, deletefunc, clone, equal, keys, values, shallow-copy, gc, UAF_SEED | 3 |
| l13_merge_iterator_4way | P0 |  | L13, merge-iterator, heap, sorted, duplicate-key, gc | 1 |
| l13_ordered_map_ops | P2 |  | L13, ordered-map, binary-search, sorted-keys, insert-delete, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_overlap_compact_ranges | P1 |  | L13, copy, overlap, compaction, record-buffer, pointer-slice, gc, UAF_SEED | 3 |
| l13_overlap_copy | P0 |  | L13, copy, overlap, memmove, pointer-slice, write-barrier, gc, UAF_SEED | 2 |
| l13_page_buffer_grow | P0 |  | L13, page-buffer, slice-growth, bytes.Buffer, truncate, gc | 1 |
| l13_pipeline_var_len | P1 |  | L13, pipeline, varint, batch, slice-growth, gc, UAF_SEED, TORTURE_ROUNDS | 1 |
| l13_prefix_trie_match | P0 |  | L13, trie, prefix-match, map, dedupe, gc, UAF_SEED | 1 |
| l13_ptr_ring_buffer | P0 |  | L13, ring-buffer, wrap, overwrite, grow, pointer, gc, UAF_SEED | 2 |
| l13_slice_append_self_overlap | P1 |  | L13, slice, append, self-overlap, growslice, full-slice-expression, pointer, gc, UAF_SEED | 3 |
| l13_slice_filter_inplace | P2 |  | L13, slice, in-place-filter, compaction, zero-tail, dedupe, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l13_slice_growth_alias | P0 |  | L13, slice, append-growth, capacity, aliasing, pointer, gc, UAF_SEED | 3 |
| l13_slice_of_slices | P1 |  | L13, slice-of-slices, jagged, aliasing, insert-delete, clone, gc, UAF_SEED | 2 |
| l13_slice_reslice_retention | P1 |  | L13, slice, reslice, capacity-retention, full-slice-expression, aliasing, zero-tail, gc, UAF_SEED | 3 |
| l13_slice_zero_elems | P2 |  | L13, slice, zero-size-element, growslice, append, reslice, copy, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_slices_concat_insert | P1 |  | L13, slices, concat, insert, replace, grow, aliasing, pointer, gc, UAF_SEED | 3 |
| l13_slices_delete_compact | P1 |  | L13, slices, delete, compact, replace, deletefunc, grow, pointer, tail-reuse, gc, UAF_SEED | 3 |
| l13_small_capacity_roundtrip | P2 |  | L13, flate, small-capacity, append-growth, roundtrip, gc, UAF_SEED, TORTURE_ROUNDS | 1 |
| l13_sort_stable_dups | P1 |  | L13, sort, stable, duplicate-key, sort.Search, pointer, gc, UAF_SEED | 3 |
| l13_string_compare_order | P0 |  | L13, string, compare, sort, binary-search, map-key, gc, UAF_SEED | 3 |
| l13_string_intern_table | P2 |  | L13, string, intern, canonical-pointer, map, purge, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l13_string_slice_bytes_gc | P0 |  | L13, string, substring, bytes, builder, gc | 1 |
| l13_strings_reader_statemachine | P0 |  | L13, strings.Reader, seek, readat, unread, readrune, writeto, reset, state-machine, gc, UAF_SEED | 3 |
| l13_struct_key_map_gc | P1 |  | L13, map, struct-key, nested-key, array-key, time-key, equality, gc, UAF_SEED | 3 |
| l13_utf8_process | P1 |  | L13, utf8, rune, invalid-sequence, map-key, gc, UAF_SEED | 2 |
| l13_utf8_truncate_boundary | P1 |  | L13, utf8, truncation, rune-boundary, invalid-sequence, map-key, gc, UAF_SEED | 3 |

## L14_debug_info — L14 调试信息（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l14_anon_func_frames | P1 |  | L14, anonymous-function, IIFE, func-literal, traceback, CallersFrames, gc | 5 |
| l14_callers_methods_closures | P1 |  | L14, CallersFrames, method-symbol, closure-symbol, pointer-receiver, gc | 4 |
| l14_callers_skip | P1 |  | L14, runtime.Callers, skip-semantics, CallersFrames, symbol-resolution, gc | 4 |
| l14_callers_skip_closure_goexit | P1 |  | L14, Callers, skip, closure, method-value, goexit, gc | 6 |
| l14_callers_skip_goexit | P1 |  | L14, Callers, skip, goexit, goroutine, traceback, gc | 5 |
| l14_callers_skip_inline | P1 |  | L14, runtime.Callers, skip-semantics, inlined-frame, CallersFrames, line-table, gc | 5 |
| l14_closure_funcname | P1 |  | L14, closure-symbol, funcval, reflect, traceback, gc | 5 |
| l14_closure_lineinfo | P1 |  | L14, closure, line-info, runtime.Caller, debug.Stack, gc | 3 |
| l14_concurrent_panic_recover | P2 |  | L14, concurrent, panic, recover, debug.Stack, goroutine, TORTURE_ROUNDS, gc | 3 |
| l14_debug_stack_large_buf | P2 |  | L14, debug.Stack, runtime.Stack, large-buffer, truncation, goroutine-dump, TORTURE_ROUNDS, gc | 3 |
| l14_deep_panic_traceback | P1 |  | L14, traceback, deep-recursion, goroutine, debug.Stack, gc | 3 |
| l14_defer_chain_panic | P2 |  | L14, defer, panic-in-defer, repanic, LIFO, traceback, TORTURE_ROUNDS, gc | 5 |
| l14_funcptr_frames | P2 |  | L14, func-pointer, method-value, reflect-funcval, traceback, TORTURE_ROUNDS, gc | 5 |
| l14_funcval_interface_dispatch | P1 |  | L14, func-value, interface-dispatch, named-func-type, func-table, traceback, gc | 5 |
| l14_gc_panic_stack | P1 |  | L14, gc, panic-stack, traceback, clobberfree, line-info | 3 |
| l14_generic_func_traceback | P1 | generics | L14, generics, instantiation, traceback, CallersFrames, gc | 5 |
| l14_generic_nested_instantiation | P1 | generics | L14, generics, instantiation, nested, traceback, CallersFrames, gc | 6 |
| l14_goroutine_dump | P1 |  | L14, goroutine-dump, stack-unwind, blocked | 1 |
| l14_goroutine_dump_created_by | P2 |  | L14, runtime.Stack, goroutine-dump, created-by, launcher-attribution, TORTURE_ROUNDS, gc | 4 |
| l14_goroutine_id_batch_lifecycle | P2 |  | L14, goroutine-id, goid-cache, lifecycle, runtime.Stack, created-by, gc | 6 |
| l14_goroutine_label_dump | P1 |  | L14, goroutine-dump, pprof-labels, runtime.Stack, created-by, gc | 5 |
| l14_goroutine_label_nested_dump | P1 |  | L14, goroutine-dump, pprof-labels, nested-labels, panic, runtime.Stack, gc | 6 |
| l14_goroutine_states_dump | P2 |  | L14, runtime.Stack, goroutine-state, chan-send, select, waitgroup, cond, TORTURE_ROUNDS, gc | 5 |
| l14_goroutine_wait_states | P2 |  | L14, runtime.Stack, goroutine-state, rwmutex, nil-channel, select-no-cases, TORTURE_ROUNDS, gc | 7 |
| l14_iface_value_receiver_method_value | P1 |  | L14, method-value, interface, value-receiver, -fm, traceback, gc | 6 |
| l14_init_closure_symbols | P1 |  | L14, closure, init, symbol, traceback, gc | 5 |
| l14_inline_callline_attribution | P1 |  | L14, inline, line-number, traceback, CallersFrames, gc | 5 |
| l14_inline_frames | P1 |  | L14, inline-frame, traceback, CallersFrames | 1 |
| l14_inline_method_line_attribution | P1 |  | L14, inline, method, runtime.Caller, line-number, traceback, gc | 7 |
| l14_interface_method_value | P1 |  | L14, method-value, interface, -fm, promoted-method, traceback, gc | 5 |
| l14_join_error_panic | P1 |  | L14, wrapped-error, errors.Join, multi-unwrap, errors.Is, errors.As, traceback, gc | 5 |
| l14_loop_callsite_traceback | P0 |  | L14, loop-lineinfo, callsite, traceback-attribution, line-table, gc | 5 |
| l14_loop_lineinfo | P2 |  | L14, line-info, loop, runtime.Caller, traceback, TORTURE_ROUNDS, gc | 5 |
| l14_method_chain_traceback | P1 |  | L14, traceback, method-symbol, pointer-receiver, embedded-method, debug.Stack, gc | 5 |
| l14_method_value_fm | P0 |  | L14, method-value, -fm-wrapper, method-expression, traceback, symbol-resolution, gc | 5 |
| l14_multi_goroutine_stack_ids | P2 |  | L14, goroutine-dump, goroutine-id, runtime.Stack, created-by, gc | 5 |
| l14_multi_init_closure_symbols | P1 |  | L14, init, closure, symbol, multiple-init, traceback, gc | 6 |
| l14_multi_recover | P1 |  | L14, multi-recover, defer, panic, debug.Stack, closure-symbol, gc | 4 |
| l14_nested_closure_symbols | P0 |  | L14, nested-closure, closure-symbol, funcval, reflect, traceback, gc | 5 |
| l14_panic_in_defer | P1 |  | L14, panic-in-defer, panic-replacement, recover, debug.Stack, gc | 4 |
| l14_stack_buf_exact_boundary | P2 |  | L14, runtime.Stack, buffer-size, exact-boundary, truncation, goroutine-dump, gc | 6 |
| l14_stack_buf_growth | P2 |  | L14, runtime.Stack, buffer-size, truncation, goroutine-dump, gc | 5 |
| l14_stack_growth_frames | P2 |  | L14, stack-growth, morestack, stack-copy, debug.Stack, CallersFrames, TORTURE_ROUNDS, gc | 5 |
| l14_stack_growth_iface_pc | P2 |  | L14, stack-growth, interface-dispatch, CallersFrames, FileLine, gc | 6 |
| l14_stack_growth_pc_capture | P2 |  | L14, stack-growth, morestack, CallersFrames, lineinfo, gc | 5 |
| l14_stack_multi_state | P1 |  | L14, runtime.Stack, goroutine-state, chan-receive, select, sleep, mutex, gc | 5 |
| l14_traceback_frames | P1 |  | L14, traceback, debug.Stack, runtime.Callers | 1 |
| l14_wrapped_error_panic | P1 |  | L14, wrapped-error, errors.Is, errors.As, panic, traceback, gc | 5 |
| l14_wrapped_panic_custom_hooks | P1 |  | L14, wrapped-error, errors.Is, errors.As, panic, traceback, gc | 5 |
| l14_wrapped_panic_repanic_hooks | P1 |  | L14, wrapped-error, errors.Is, errors.As, repanic, traceback, gc | 7 |

## L15_build_tags — L15 构建标签与多平台（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l15_arch_mutex | P1 | pkgbuild | L15, build-tag, GOARCH, mutual-exclusion, init, file-selection, gc | 5 |
| l15_build_expr_legacy | P1 | pkgbuild | L15, build-tag, build-expression, legacy-plusbuild, AND, OR, NOT, precedence, parentheses, gc | 8 |
| l15_build_expr_nested | P1 | pkgbuild | L15, build-tag, AND, OR, NOT, parentheses, custom-tags, tautology, gc | 6 |
| l15_buildtag_arch | P1 | pkgbuild | L15, filename-suffix, GOARCH, file-selection | 1 |
| l15_buildtag_custom | P1 | pkgbuild | L15, build-tag, custom-tags | 1 |
| l15_buildtag_os | P1 | pkgbuild | L15, build-tag, GOOS, file-selection | 1 |
| l15_custom_tag_dep | P1 | pkgbuild | L15, build-tag, custom-tags, tag-dependency, init | 4 |
| l15_cycle_goarch_edges | P2 | pkgbuild | L15, build-tag, GOARCH, filename-suffix, import-cycle, init-order, TORTURE_ROUNDS, gc | 7 |
| l15_excluded_scope_boundary | P1 | pkgbuild | L15, build-tag, file-selection, test-file, embed, parse-order, gc | 6 |
| l15_ext_case_and_dir | P0 | pkgbuild | L15, file-selection, filename, extension, build-scope, gc | 9 |
| l15_file_init_lexical | P1 | pkgbuild | L15, build-tag, init-order, file-name-order, GOOS, gc | 6 |
| l15_filename_goos | P1 | pkgbuild | L15, filename-suffix, GOOS, GOARCH, file-selection | 4 |
| l15_filename_suffix_matrix | P0 | pkgbuild | L15, filename-suffix, GOOS, GOARCH, file-selection, unix-meta-tag, gc | 7 |
| l15_goos_init | P1 | pkgbuild | L15, GOOS, init-order, build-tag, file-selection | 4 |
| l15_goos_suffix_tag | P1 | pkgbuild | L15, filename-suffix, GOOS, build-tag, conjunction, custom-tags, gc | 5 |
| l15_ignored_extensions | P0 | pkgbuild | L15, file-selection, filename, build-scope, gc | 6 |
| l15_init_phase_order | P1 | pkgbuild | L15, build-tag, init-order, package-variable, file-name-order, GOOS, gc | 7 |
| l15_init_tag_edge | P1 | pkgbuild | L15, build-tag, init-order, GOOS, multi-package, dependency-edge, gc | 6 |
| l15_layout_pointer_arch | P2 | pkgbuild | L15, build-tag, GOARCH, struct-layout, unsafe, reflection, TORTURE_ROUNDS, gc | 6 |
| l15_main_arch_entry | P1 | pkgbuild | L15, build-tag, GOARCH, filename-suffix, main-entry, mutual-exclusion, gc | 7 |
| l15_module_multipkg | P1 | pkgbuild | L15, module, multi-package, cross-package-link, subpackage-build-tag, init-order, gc | 5 |
| l15_module_tag_var_chain | P2 | pkgbuild | L15, module, multi-package, package-variable, build-tag, GOOS, init-order, TORTURE_ROUNDS, gc | 6 |
| l15_module_var_deps | P1 | pkgbuild | L15, module, multi-package, package-variable, init-order, dependency-chain, gc | 6 |
| l15_multipkg_tags | P1 | pkgbuild | L15, module, multi-package, custom-tags, GOOS, cross-package-link, gc | 5 |
| l15_mutex_files | P1 | pkgbuild | L15, build-tag, mutual-exclusion, file-selection, init | 4 |
| l15_osarch_combo_tag | P1 | pkgbuild | L15, build-tag, GOOS, GOARCH, AND, OR, NOT, file-selection, gc | 5 |
| l15_pkg_init_order | P1 | pkgbuild | L15, module, multi-package, init-order, package-variable, dependency-order, gc | 6 |
| l15_sentinel_case_suffix | P0 | pkgbuild | L15, build-tag, tag-vocabulary, case-sensitivity, ignore, filename-suffix, gc | 9 |
| l15_suffix_edge_segments | P1 | pkgbuild | L15, filename-suffix, GOOS, GOARCH, case-sensitivity, parse-order, gc | 9 |
| l15_suffix_parse_order | P1 | pkgbuild | L15, filename-suffix, GOOS, GOARCH, file-selection, parse-order, gc | 7 |
| l15_suffix_same_partition | P1 | pkgbuild | L15, filename-suffix, build-tag, GOOS, GOARCH, conjunction, mutual-exclusion, gc | 8 |
| l15_suffix_tag_intersection | P1 | pkgbuild | L15, filename-suffix, build-tag, GOOS, GOARCH, conjunction, mutual-exclusion, gc | 6 |
| l15_tag_and_or_not | P1 | pkgbuild | L15, build-tag, AND, OR, NOT, GOOS, GOARCH, custom-tags | 5 |
| l15_tag_const_iota | P1 | pkgbuild | L15, build-tag, GOOS, constant, iota, typed-constant, array-length, constant-expression, gc | 5 |
| l15_tag_const_methodset | P1 | pkgbuild | L15, build-tag, constant, method-set, reflect, interface, custom-tags | 5 |
| l15_tag_const_select | P1 | pkgbuild | L15, build-tag, constant, compile-time-array, constant-expression, gc | 5 |
| l15_tag_cycle_break | P2 | pkgbuild | L15, build-tag, multi-package, import-cycle, GOOS, init-order, TORTURE_ROUNDS, gc | 7 |
| l15_tag_excluded_unparsed | P1 | pkgbuild | L15, build-tag, file-selection, parse-order, duplicate-symbol, gc | 6 |
| l15_tag_iface_impl | P1 | pkgbuild | L15, build-tag, interface, implementation-selection, reflection, gc | 5 |
| l15_tag_ignore_sentinel | P0 | pkgbuild | L15, build-tag, tag-vocabulary, negation, ignore, file-selection, gc | 6 |
| l15_tag_import_graph | P1 | pkgbuild | L15, build-tag, multi-package, import-graph, GOOS, init-order, cross-package-link, gc | 6 |
| l15_tag_main_variant | P1 | pkgbuild | L15, build-tag, GOOS, main-entry, mutual-exclusion, gc | 5 |
| l15_tag_methodset_subpkg | P1 | pkgbuild | L15, build-tag, method-set, interface, reflection, multi-package, gc | 6 |
| l15_tag_struct_layout | P2 | pkgbuild | L15, build-tag, GOOS, struct-layout, unsafe, reflection, TORTURE_ROUNDS, gc | 6 |
| l15_tag_var_selection | P2 | pkgbuild | L15, build-tag, selection-method, function-variable, init-registry, generics, custom-tags, TORTURE_ROUNDS, gc | 5 |
| l15_tag_vocab_bitset | P1 | pkgbuild | L15, build-tag, GOOS, GOARCH, unix-meta-tag, compiler-tag, release-tag, tag-vocabulary, gc | 7 |
| l15_underscore_ignored | P0 | pkgbuild | L15, build-constraint, underscore-ignore, dotfile-ignore, test-file-exclusion, file-selection, gc | 7 |
| l15_unix_arch_partition | P2 | pkgbuild | L15, build-tag, GOOS, GOARCH, unix-meta-tag, partition, TORTURE_ROUNDS, gc | 5 |
| l15_var_func_dep_files | P2 | pkgbuild | L15, package-variable, init-order, dependency-order, build-tag, GOOS, TORTURE_ROUNDS, gc | 8 |
| l15_var_init_dep_files | P2 | pkgbuild | L15, package-variable, init-order, dependency-order, build-tag, GOOS, TORTURE_ROUNDS, gc | 6 |

## L16_template_interp — L16 模板/解释器/元编程（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l16_ast_comment_rewrite | P1 | goast | L16, goast, ast-rewrite, comment-map, go-format, go-types, idempotent, GC | 5 |
| l16_ast_inspect_path | P1 | goast | L16, goast, ast-inspect, path-stack, shadowing, enclosing-func, pruning, GC | 4 |
| l16_ast_path_defuse | P1 | goast | L16, goast, ast-inspect, path-stack, defs-uses, scopes, selections, GC | 4 |
| l16_ast_rewrite | P2 | goast | L16, goast, go-parser, go-format, ast-rewrite, go-types, GC, TORTURE_ROUNDS | 5 |
| l16_ast_walk | P1 | goast | L16, goast, go-parser, ast-walk, node-count, GC | 2 |
| l16_bytecode_vm | P2 | goast | L16, goast, bytecode, stack-vm, go-parser, control-flow, recursion, step-limit, GC, TORTURE_ROUNDS | 6 |
| l16_const_convert_round | P1 | goast | L16, goast, go-constant, go-types, rounding, float32, float64, overflow, GC | 3 |
| l16_constant_ops | P2 | goast | L16, goast, go-constant, arbitrary-precision, rational, shifts, conversions, GC, TORTURE_ROUNDS | 4 |
| l16_constant_rounding | P1 | goast | L16, goast, go-constant, arbitrary-precision, rounding, float32, exact-compare, GC | 4 |
| l16_css_js_url_escape | P2 | templates | L16, templates, html-template, css-escape, js-escape, url-escape, srcset, safe-types, GC, TORTURE_ROUNDS | 4 |
| l16_expr_eval | P1 | goast | L16, goast, go-parser, go-types, constant-folding, expression-eval, GC | 3 |
| l16_funcmap_error | P1 | templates | L16, templates, FuncMap, error-propagation, nested-template, GC | 5 |
| l16_funcmap_panic_signature | P1 | templates | L16, templates, FuncMap, panic, invalid-signature, nil-function, variadic, error-text, GC | 5 |
| l16_funcmap_pointer | P1 | templates | L16, templates, FuncMap, pointers, variadic, GC | 3 |
| l16_html_clone_context | P2 | templates | L16, templates, html-template, clone, addparsetree, escaping, nested-context, concurrency, TORTURE_ROUNDS, GC | 6 |
| l16_html_context_edge | P1 | templates | L16, templates, html-template, escaping, rcdata, css-url, js-context, srcset, urlquery, GC | 6 |
| l16_html_escape | P2 | templates | L16, templates, html-template, escaping, contexts, GC, TORTURE_ROUNDS | 3 |
| l16_html_nested_context | P2 | templates | L16, templates, html-template, nested-template, block, context-propagation, safe-types, GC, TORTURE_ROUNDS | 5 |
| l16_html_vs_text_escape | P1 | templates | L16, templates, html-template, text-template, auto-escaping, safe-types, url-filter, GC | 4 |
| l16_interp_closure_loop | P2 | goast | L16, goast, interpreter, closures, loop-variable, recursion, environment-capture, TORTURE_ROUNDS, GC | 4 |
| l16_interp_closures | P2 | goast | L16, goast, interpreter, closures, environment-capture, higher-order, step-limit, depth-limit, GC, TORTURE_ROUNDS | 4 |
| l16_method_templates | P1 | templates | L16, templates, methods, pointer-receiver, pointer-return, GC | 2 |
| l16_mini_interp | P2 | goast | L16, goast, interpreter, recursion, go-parser, step-limit, GC, TORTURE_ROUNDS | 5 |
| l16_nested_method_block | P2 | templates | L16, templates, nested-template, block, methods, pointer-receiver, method-error, GC, TORTURE_ROUNDS | 5 |
| l16_nested_templates | P2 | templates | L16, templates, define, template-nesting, order-independence, GC, TORTURE_ROUNDS | 2 |
| l16_parse_errors | P0 | templates | L16, templates, parse-error, error-line, html-template, escape-context, GC | 5 |
| l16_parse_line_mapping | P0 | templates | L16, templates, parse-error, line-mapping, crlf, unicode, template-must, execute-error, GC | 4 |
| l16_parser_recover | P0 | goast | L16, goast, go-parser, error-recovery, error-list, incremental-parse, comments, GC | 3 |
| l16_parser_recover_scope | P0 | goast | L16, goast, go-parser, error-recovery, go-types, scopes, uses, GC | 4 |
| l16_pipeline_clone_bind | P2 | templates | L16, templates, pipeline, clone, FuncMap, option-isolation, TORTURE_ROUNDS, GC | 5 |
| l16_positions | P0 | goast | L16, goast, go-token, positions, line-directive, GC | 3 |
| l16_range_if_ptr_slice | P1 | templates | L16, templates, text-template, range, nil-pointer, if-with-else, break-continue, GC | 5 |
| l16_range_map_sorted_ptr | P2 | templates | L16, templates, range, map-sort, pointer-slice, nil-pointer, missing-key, GC, TORTURE_ROUNDS | 4 |
| l16_reflect_call | P1 | reflect | L16, reflect, reflect-call, pointer-args, variadic, MakeFunc, GC | 3 |
| l16_reflect_iter_value | P1 | reflect | L16, reflect, value-seq, seq2, map-iter, panic, GC | 4 |
| l16_reflect_method_call | P1 | reflect | L16, reflect, method-value, method-expression, embedding, interface-dispatch, variadic, GC | 5 |
| l16_reflect_struct_tag | P0 | reflect | L16, reflect, struct-tag, json, embedded-field, unexported-field, GC | 4 |
| l16_reflect_typed_nil_method | P1 | reflect | L16, reflect, typed-nil, interface, method-value, method-expression, receiver-capture, panic, GC | 5 |
| l16_reflect_value_ops | P1 | reflect | L16, reflect, deep-copy, map-range, slice-ops, struct-of, convert, comparable, GC | 4 |
| l16_text_template_fields | P0 | templates | L16, templates, text-template, pointer-fields, GC | 3 |
| l16_tmpl_clone_cache | P2 | templates | L16, templates, clone, template-cache, FuncMap, option-isolation, concurrency, GC, TORTURE_ROUNDS | 3 |
| l16_tmpl_pipeline_builtins | P1 | templates | L16, templates, pipeline, builtins, FuncMap, dict, short-circuit, GC | 4 |
| l16_types_check | P1 | goast | L16, goast, go-types, type-check, diagnostics, method-set, GC | 2 |
| l16_types_const_overflow | P1 | goast | L16, goast, go-types, go-constant, arbitrary-precision, shifts, overflow, representability, GC | 6 |
| l16_types_generic_methodset | P1 | goast | L16, goast, go-types, generics, instantiation, method-set, embedding, interface, constraints, GC | 4 |
| l16_types_methodset | P1 | goast | L16, goast, go-types, method-set, embedding, interface, MissingMethod, GC | 5 |
| l16_types_scope_generic | P1 | goast | L16, goast, go-types, generics, type-params, instances, scopes, methodsets, GC | 4 |
| l16_types_scope_lookup | P1 | goast | L16, goast, go-types, scopes, defs-uses, implicits, selections, shadowing, GC | 3 |
| l16_vars_expand | P0 |  | L16, scanner, brace-expand, unicode, error-position, determinism, GC | 4 |
| l16_vars_expand_parse | P0 | templates | L16, templates, vars-expand, text-template, brace-expansion, injection, error-position, GC | 3 |

## L17_stackmap — L17 栈映射（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l17_addrtaken_local_array | P0 |  | L17, array, address-taken, zero-point | 1 |
| l17_chan_pointer_handoff | P1 |  | L17, stackmap, chan, unbuffered, noinline, GC | 1 |
| l17_chan_struct_iface | P1 |  | L17, stackmap, channel, struct-by-value, any, func-field, map, close-range, GC, UAF_SEED | 2 |
| l17_chan_struct_ptr | P1 |  | L17, stackmap, channel, struct-by-value, goroutine, interface-boxing, GC, UAF_SEED | 2 |
| l17_chan_struct_signal | P2 |  | L17, stackmap, chan-struct, broadcast, close, goroutine-stack, noinline, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_chan_struct_token_ring | P2 |  | L17, stackmap, chan-struct, token-ring, goroutine-stack, close-broadcast, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_closure_capture_escape | P1 |  | L17, stackmap, closure, closure-table, goroutine, per-iteration, nested-closure, GC, UAF_SEED | 2 |
| l17_closure_cell_rebind_chain | P1 |  | L17, stackmap, closure, mutated-capture, shared-cell, interface-dispatch, GC, UAF_SEED | 2 |
| l17_closure_defer_combo | P1 |  | L17, stackmap, closure, defer, captured-pointer, per-iteration, GC | 2 |
| l17_closure_mutated_capture | P1 |  | L17, stackmap, closure, closure-table, mutated-capture, interface-boxing, noinline, GC, UAF_SEED | 2 |
| l17_defer_iface_method | P0 |  | L17, stackmap, defer, interface-method, itab, deferreturn, GC | 2 |
| l17_defer_panic_unwind | P0 |  | L17, defer, panic, recover, unwind | 1 |
| l17_embed_wrapper_aggregate | P1 |  | L17, stackmap, embedding, wrapper, promoted-method, aggregate-by-value, interface, GC | 2 |
| l17_fca_defer_multiarg | P1 |  | L17, stackmap, FCA, nested-aggregate, multiarg-defer, byval-copy, any-field, GC, UAF_SEED | 2 |
| l17_fca_nested_defer_arg | P2 |  | L17, stackmap, FCA, nested-aggregate, defer-record, byval-return, any-field, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_fca_nested_mixed | P1 |  | L17, stackmap, FCA, nested-aggregate, any-field, func-field, map-field, byval-return, GC, UAF_SEED | 2 |
| l17_fca_return_byval | P0 |  | L17, FCA, aggregate, byval | 1 |
| l17_iface_defer_nested | P2 |  | L17, stackmap, interface, nested-interface, defer, method-expression, nil-receiver, recover, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_iface_defer_variadic | P0 |  | L17, stackmap, defer, interface-method, variadic, defer-record, method-expression, GC, UAF_SEED | 2 |
| l17_iface_defer_variadic_edge | P0 |  | L17, stackmap, defer, interface-method, variadic-boundary, nil-slice, method-value, mutated-backing-array, GC, UAF_SEED | 2 |
| l17_inline_boundary | P0 |  | L17, inline, noinline, aggregate | 1 |
| l17_method_named_defer | P0 |  | L17, stackmap, method, named-return, defer, deferreturn, method-value, GC | 2 |
| l17_method_named_defer_chain | P0 |  | L17, stackmap, method, named-return, defer-chain, panic, recover, LIFO, GC, UAF_SEED | 2 |
| l17_method_named_reflect_sink | P1 |  | L17, stackmap, method, named-return, defer-sink, reflect, interface-dispatch, GC, UAF_SEED | 2 |
| l17_method_wrapper_tail | P0 |  | L17, method-value, wrapper, tail-call | 1 |
| l17_morestack_bigframe | P1 |  | L17, morestack, stack-copy, recursion | 1 |
| l17_named_ret_method_sink | P1 |  | L17, stackmap, method, named-return, defer, deferreturn, method-value, escaped-result, GC, UAF_SEED | 2 |
| l17_named_return_defer | P0 |  | L17, defer, named-return, panic | 1 |
| l17_recursive_closure_gc | P2 |  | L17, stackmap, recursive-closure, tree, parent-pointer, GC, TORTURE_ROUNDS, UAF_SEED | 2 |
| l17_select_defer_cases | P2 |  | L17, stackmap, select, send-case, closed-channel, nil-channel, defer-in-case, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_select_defer_interleave | P2 |  | L17, stackmap, select, defer, chan-of-struct, per-iteration-capture, GC, TORTURE_ROUNDS, UAF_SEED | 2 |
| l17_select_struct_iface_notify | P2 |  | L17, stackmap, select, chan-struct, interface-variadic, nested-select, nil-channel, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_select_struct_notify | P2 |  | L17, stackmap, select, chan-struct, closed-channel, nil-channel, defer-in-case, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_slice_struct_byval | P1 |  | L17, stackmap, byval, aggregate, slice-of-struct, string | 1 |
| l17_spill_iface_matrix | P2 |  | L17, stackmap, spill, interface-matrix, register-abi, morestack, non-empty-interface, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_spill_iface_pair | P1 |  | L17, stackmap, spill, register-abi, interface-pair, struct-by-value, morestack, noinline, GC, UAF_SEED | 2 |
| l17_spill_local_pressure | P1 |  | L17, stackmap, spill, register-abi, mixed-args, local-pressure, pointer-array, GC, UAF_SEED | 2 |
| l17_spill_many_ptr_args | P2 |  | L17, stackmap, spill, register-abi, many-args, pointer-args, GC, TORTURE_ROUNDS, UAF_SEED | 2 |
| l17_stack_ptr_across_call | P0 |  | L17, stackmap, noinline, chan, interface | 1 |
| l17_struct_ret_defer_pair | P1 |  | L17, stackmap, struct-return, defer-record, multiple-defers, method-value, by-value-copy, GC, UAF_SEED | 2 |
| l17_struct_return_defer_arg | P0 |  | L17, stackmap, aggregate-return, struct-by-value, defer-record, noinline, GC, UAF_SEED | 2 |
| l17_struct_return_fields | P1 |  | L17, stackmap, aggregate-return, struct-by-value, interface, method, GC | 2 |
| l17_struct_return_regabi | P0 |  | L17, stackmap, aggregate-return, register-abi, multiple-return, interface, GC, UAF_SEED | 2 |
| l17_tailcall_mutual | P2 |  | L17, stackmap, tail-call, mutual-recursion, interface-dispatch, closure-recursion, stack-growth, TORTURE_ROUNDS, GC, UAF_SEED | 2 |
| l17_tailcall_variadic_accum | P1 |  | L17, stackmap, tail-call, mutual-recursion, interface-dispatch, variadic, accumulator, defer-unwind, GC, UAF_SEED | 2 |
| l17_tailcall_variadic_edge | P0 |  | L17, stackmap, tail-call, mutual-recursion, interface-dispatch, variadic, boundary-depth, nil-spread, GC, UAF_SEED | 2 |
| l17_variadic_iface_boundary | P1 |  | L17, stackmap, variadic, interface-forwarding, nil-spread, reslice, boundary, GC, UAF_SEED | 2 |
| l17_variadic_iface_forward | P0 |  | L17, stackmap, variadic, interface, forwarding, noinline, GC, UAF_SEED | 2 |
| l17_variadic_iface_ptr | P1 |  | L17, stackmap, variadic, interface, []any, method, GC, UAF_SEED | 2 |
| l17_variadic_iface_slices | P0 |  | L17, stackmap, variadic, interface, reslice, deferred-closure, GC, UAF_SEED | 2 |

## L18_gc_lifecycle_wb — L18 GC 生命周期与写屏障（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l18_big_struct_array_zero | P2 |  | L18, large-object, zeroing, clear, struct-array, write-barrier, TORTURE_ROUNDS | 2 |
| l18_big_struct_overlap_copy | P2 |  | L18, large-object, struct-copy, overlapping-copy, zeroing, write-barrier, TORTURE_ROUNDS | 2 |
| l18_chan_of_chans_fanout | P1 |  | L18, chan-of-chans, buffered-channel, unbuffered-channel, packet-chain, GC, UAF_SEED | 3 |
| l18_closed_chan_buffer_gc | P1 |  | L18, channel, close, buffered, young-payload, GC, UAF_SEED | 2 |
| l18_cross_generation_ladder | P2 |  | L18, cross-generation, old-to-young, young-to-old, map, interface, ladder, TORTURE_ROUNDS | 3 |
| l18_cross_generation_mix | P1 |  | L18, write-barrier, cross-generation, old-to-young, young-to-old, interface, cycle, noinline, UAF_SEED | 2 |
| l18_finalizer_chain_release | P1 |  | L18, finalizer, chain, reachability, gc-poll | 2 |
| l18_finalizer_graph_cohorts | P1 |  | L18, finalizer, parent-child, liveness, shared-child, noinline | 2 |
| l18_finalizer_queue_drain | P1 |  | L18, finalizer, queue-channel, reachability, gc-poll, UAF_SEED | 2 |
| l18_finalizer_reachable | P1 |  | L18, finalizer, liveness, probe | 1 |
| l18_global_array_gen_rotate | P2 |  | L18, global-array, write-barrier, interface, slice, generation-rotation, noinline, TORTURE_ROUNDS | 2 |
| l18_global_array_struct_ptrs | P2 |  | L18, write-barrier, global-array, struct-array, pointer-fields, UAF_SEED, TORTURE_ROUNDS | 2 |
| l18_global_iface_slice_rotate | P2 |  | L18, global-root, interface-slice, map-interface, generation-rotation, noinline, TORTURE_ROUNDS | 3 |
| l18_global_ptr_array_churn | P1 |  | L18, write-barrier, global-array, pointer-churn, UAF_SEED | 2 |
| l18_global_roots | P0 |  | L18, globals, gcprog, map, slice, interface | 1 |
| l18_goroutine_stack_handoff | P2 |  | L18, goroutine-stack, channel-handoff, stack-growth, noinline, TORTURE_ROUNDS | 2 |
| l18_graph_detach_reattach | P2 |  | L18, object-graph, nested-map, slice, interface, detach-reattach, old-to-old, TORTURE_ROUNDS | 3 |
| l18_heap_zero_churn | P1 |  | L18, zero-init, FreeOSMemory, large-object | 1 |
| l18_huge_struct_zero_assign | P2 |  | L18, large-object, zeroing, huge-struct, value-copy, clear, noinline, TORTURE_ROUNDS | 2 |
| l18_iobuf_pool_reuse_gc | P1 |  | L18, sync.Pool, reuse, size-class, marker, GC | 1 |
| l18_keepalive_boundary | P1 |  | L18, keepalive, liveness, finalizer, gc-boundary | 1 |
| l18_keepalive_multi_objects | P1 |  | L18, keepalive, liveness, finalizer, interface, slice, loop | 2 |
| l18_keepalive_nested_calls | P1 |  | L18, KeepAlive, finalizer, noinline-call-chain, defer, interface, UAF_SEED | 2 |
| l18_large_obj_young_owner | P2 |  | L18, large-object, young-slice, old-interface, zeroing, noinline, TORTURE_ROUNDS | 2 |
| l18_many_finalizers | P2 |  | L18, finalizer, liveness, mass-objects, TORTURE_ROUNDS | 1 |
| l18_map_clear_refill | P0 |  | L18, write-barrier, map-clear, refill, old-map, noinline, UAF_SEED, TORTURE_ROUNDS | 2 |
| l18_map_old_key_new_value_grow | P2 |  | L18, write-barrier, map-growth, old-key, young-value, UAF_SEED, TORTURE_ROUNDS | 1 |
| l18_map_ptr_key_churn | P0 |  | L18, write-barrier, map, pointer-key, delete-reinsert, noinline, UAF_SEED, TORTURE_ROUNDS | 2 |
| l18_map_slice_iface_triple_wb | P1 |  | L18, write-barrier, map, slice, interface, noinline, UAF_SEED | 3 |
| l18_nested_graph_rewire | P1 |  | L18, write-barrier, nested-graph, map, slice, interface, cycle, noinline, UAF_SEED | 2 |
| l18_old_chain_bridge_graft | P0 |  | L18, write-barrier, old-to-young, doubly-linked-list, noinline, UAF_SEED | 2 |
| l18_old_chain_splice_rotate | P1 |  | L18, write-barrier, old-chain, doubly-linked-list, noinline, UAF_SEED | 2 |
| l18_old_chain_two_hop | P1 |  | L18, write-barrier, old-to-old-to-young, pointer-chain, noinline, gc | 2 |
| l18_old_chan_composite_ring | P1 |  | L18, write-barrier, channel, old-chan, buffered, unbuffered, ring-buffer, composite-value | 2 |
| l18_old_chan_recv_young | P1 |  | L18, write-barrier, channel, producer-consumer, old-chan, gc | 1 |
| l18_old_interface_hot_swap | P1 |  | L18, write-barrier, interface, itab, hot-swap, noinline, gc | 2 |
| l18_old_slice_bulk_rewrite | P1 |  | L18, write-barrier, old-slice, bulk-rewrite, copy, UAF_SEED | 1 |
| l18_old_string_field_swap | P0 |  | L18, write-barrier, old-to-young, string, byte-slice, interface, noinline, UAF_SEED, TORTURE_ROUNDS | 2 |
| l18_old_to_young_append_chan | P0 |  | L18, write-barrier, append, chan, old-gen | 1 |
| l18_old_to_young_field | P0 |  | L18, write-barrier, old-gen, interface, map, slice | 1 |
| l18_overlap_ptr_array_copy | P1 |  | L18, write-barrier, overlapping-copy, pointer-array, interface-array, string-array, noinline, UAF_SEED | 2 |
| l18_partial_message_recovery | P0 |  | L18, framing, partial-read, buffer-lifecycle, GC | 1 |
| l18_pool_finalizer_chan_handoff | P1 |  | L18, sync.Pool, finalizer, channel, reachability, GC, UAF_SEED | 3 |
| l18_pool_finalizer_combo | P1 |  | L18, sync.Pool, finalizer, liveness, gc | 2 |
| l18_pool_finalizer_release_cycle | P2 |  | L18, sync.Pool, finalizer, liveness, release-cycle, noinline, TORTURE_ROUNDS | 2 |
| l18_pool_liveness | P0 |  | L18, sync.Pool, external-reference, gc | 1 |
| l18_slice_backing_swap | P1 |  | L18, write-barrier, slice-header, backing-array, retired-root, reslice-append, noinline, UAF_SEED, TORTURE_ROUNDS | 2 |
| l18_stream_cancel_reuse | P1 |  | L18, state-machine, cancel, reuse, lifecycle | 1 |
| l18_triple_wb_delete_reinsert | P0 |  | L18, write-barrier, map, slice, interface, delete-reinsert, slice-reuse, noinline, UAF_SEED | 2 |
| l18_value_delete_rewrite_gc | P1 |  | L18, write-barrier, large-value, delete, rewrite, UAF_SEED | 1 |

## L19_netpoll_timer_signal — L19 netpoll/定时器/信号（50）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l19_after_timeout_select | P0 |  | L19, select, time-after, timeout, pointer, gc | 2 |
| l19_afterfunc_reset | P0 |  | L19, afterfunc, timer-reset, callback-state, pointer, gc, UAF_SEED | 3 |
| l19_afterfunc_self_rearm | P1 |  | L19, afterfunc, self-reset, callback, stop, timer-heap, pointer, gc, UAF_SEED | 3 |
| l19_afterfunc_self_reset_zero | P0 |  | L19, afterfunc, zero-duration, negative-duration, self-reset, timer-heap, pointer, gc, UAF_SEED | 3 |
| l19_conn_deadline_exceeded | P1 | net | L19, deadline, deadline-exceeded, tcp, net-pipe, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_context_timeout_path | P1 |  | L19, context, with-timeout, cancel, afterfunc, timer, pointer, gc, UAF_SEED | 3 |
| l19_goroutine_block_io | P2 | net | L19, blocking-io, net-pipe, os-pipe, goroutine, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_io_context_cancel | P1 | net | L19, context, cancel, afterfunc, net-pipe, blocking-io, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_multi_timer_cancel | P1 |  | L19, timer-cancel, stop, concurrent, afterfunc, pointer, gc, UAF_SEED | 3 |
| l19_netpipe_chain | P1 | net | L19, net-pipe, multi-hop, netpoll, blocking-io, pointer, gc, UAF_SEED | 3 |
| l19_netpipe_halfclose | P1 | net | L19, net-pipe, half-close, fin, eof, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_netpipe_multi_writer | P2 | net | L19, net-pipe, multi-writer, netpoll, blocking-io, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_netpipe_multi_writer_partial | P2 | net | L19, net-pipe, multi-writer, partial-read, reassembly, netpoll, blocking-io, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_netpipe_payload | P1 | net | L19, net-pipe, netpoll, goroutine, pointer, gc, close, UAF_SEED | 2 |
| l19_os_pipe_bulk | P2 |  | L19, os-pipe, blocking-io, bulk-data, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_os_pipe_partial_epipe | P2 |  | L19, os-pipe, blocked-write, epipe, partial-write, reader-close, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_os_pipe_poll | P1 |  | L19, os-pipe, blocking-io, netpoll, pointer, gc, eof, UAF_SEED | 2 |
| l19_os_pipe_write_cancel | P1 |  | L19, os-pipe, write-cancel, blocked-write, epipe, pointer, gc, UAF_SEED | 3 |
| l19_pipe_read_cancel | P1 |  | L19, io-pipe, os-pipe, read-cancel, closewitherror, eof, sticky-error, pointer, gc, UAF_SEED | 3 |
| l19_select_default_poll | P0 |  | L19, select-default, nonblocking, channel-polling, pointer, gc, deterministic | 3 |
| l19_signal_ctx_afterfunc | P1 | signals, unix | L19, signal, notify-context, context-afterfunc, cancel, worker-drain, sigusr1, sigusr2, pointer, gc, linux-only | 3 |
| l19_signal_ignore_reset | P1 | signals, unix | L19, signal, signal-ignore, signal-reset, sigusr1, sigusr2, pointer, gc, linux-only | 2 |
| l19_signal_multi_chan | P1 | signals, unix | L19, signal, multi-channel, notify, stop, sigusr1, sigusr2, pointer, gc, linux-only | 3 |
| l19_signal_notify | P1 | signals, unix | L19, signal, sigusr1, syscall-kill, pointer, gc, linux-only | 1 |
| l19_signal_notify_context | P1 | signals, unix | L19, signal, notify-context, context-cancel, sigusr1, sigusr2, pointer, gc, linux-only | 3 |
| l19_signal_timer_select | P1 | signals, unix | L19, signal, timer, select, sigusr1, stop, pointer, gc, linux-only | 3 |
| l19_signal_timer_shutdown | P1 | signals, unix | L19, signal, timer, afterfunc-watchdog, select, graceful-shutdown, sigusr1, pointer, gc, linux-only | 3 |
| l19_tcp_accept_churn | P2 | net | L19, tcp, accept, deadline, listener-close, churn, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_tcp_accept_multi_close | P1 | net | L19, tcp, accept, multi-accept, listener-close, errclosed, dial-after-close, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_tcp_accept_timeout | P1 | net | L19, tcp, accept, deadline, timeout, listener, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_tcp_dial_cancel | P1 | net | L19, tcp, dial, context-cancel, deadline, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_tcp_dial_cancel_many | P1 | net | L19, tcp, dial-cancel, context-cancel, deadline, accept-queue, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_tcp_halfclose | P1 | net | L19, tcp, half-close, closewrite, fin, eof, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_tcp_halfclose_bidir | P1 | net | L19, tcp, half-close, closewrite, simultaneous-fin, eof, netpoll, pointer, gc, UAF_SEED | 3 |
| l19_tcp_loopback | P1 | net | L19, tcp, loopback, netpoll, echo, pointer, gc, UAF_SEED | 2 |
| l19_ticker_select_merge | P2 |  | L19, ticker, select, event-loop, stop, channel, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_ticker_stop_reset | P1 |  | L19, ticker, stop, reset, concurrent-stop, pointer, gc, UAF_SEED | 3 |
| l19_ticker_worker | P1 |  | L19, ticker, worker-loop, pointer, gc, stop, UAF_SEED | 2 |
| l19_timer_boundary_gc | P1 |  | L19, afterfunc, timer-heap, liveness, unique-reference, pointer, gc, UAF_SEED | 2 |
| l19_timer_coalesce_cancel | P2 |  | L19, timer-reset, coalescing, batch-cancel, afterfunc, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_timer_heap_grow_shrink | P2 |  | L19, timer-heap, afterfunc, stop, growth, shrink, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_timer_heap_many | P2 |  | L19, timer-heap, afterfunc, closure, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_timer_many_waiters | P2 |  | L19, timer-heap, concurrent-waiters, stop, select, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_timer_negative_zero | P0 |  | L19, timer, zero-duration, negative-duration, reset, stop, panic, pointer, gc, UAF_SEED | 3 |
| l19_timer_panic_recover | P1 |  | L19, afterfunc, timer-callback, panic, recover, pointer, gc, UAF_SEED | 3 |
| l19_timer_stop_reset | P0 |  | L19, timer, stop, reset, pointer, gc, deterministic | 2 |
| l19_timer_zero_batch_gc | P2 |  | L19, afterfunc, zero-duration, timer-heap, batch, stop-after-fire, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_udp_deadline_churn | P2 | net | L19, udp, deadline, read-deadline, write-deadline, close, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_udp_deadline_cycles | P2 | net | L19, udp, read-deadline, write-deadline, deadline-preserves-queue, close-wakeup, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_udp_loopback | P2 | net | L19, udp, loopback, netpoll, echo, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |

