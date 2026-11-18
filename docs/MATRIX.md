# 用例覆盖矩阵（自动生成）

> 由 `tools/gen_matrix.py` 扫描 `cases/*/*/case.md` 生成；不要手改。

## 维度汇总

| 维度 | 名称 | 用例数 | P0 | P1 | P2 | 目标 | 状态 |
|---|---|---:|---:|---:|---:|---:|---|
| L01_generics | L1 泛型 | 20 | 8 | 6 | 6 | 50 | 🔶 |
| L02_concurrency | L2 高并发 | 20 | 5 | 9 | 6 | 50 | 🔶 |
| L03_async_preempt | L3 异步抢占 | 20 | 5 | 10 | 5 | 50 | 🔶 |
| L04_scheduler_stack | L4 协程调度/栈 | 20 | 2 | 13 | 5 | 50 | 🔶 |
| L05_alloc_gc | L5 大量内存分配 | 20 | 4 | 11 | 5 | 50 | 🔶 |
| L06_reflect_iface | L6 反射与接口 | 20 | 4 | 11 | 5 | 50 | 🔶 |
| L07_unsafe_zerocopy | L7 unsafe 与零拷贝 | 20 | 7 | 8 | 5 | 50 | 🔶 |
| L08_asm_simd | L8 汇编与 SIMD | 20 | 0 | 20 | 0 | 50 | 🔶 |
| L09_cgo | L9 cgo | 20 | 0 | 20 | 0 | 50 | 🔶 |
| L10_codegen | L10 代码生成 | 20 | 4 | 10 | 6 | 50 | 🔶 |
| L11_compiler_opts | L11 编译期优化 | 20 | 7 | 8 | 5 | 50 | 🔶 |
| L12_init_link | L12 初始化与链接 | 20 | 5 | 10 | 5 | 50 | 🔶 |
| L13_runtime_ds | L13 运行时数据结构 | 20 | 11 | 5 | 4 | 50 | 🔶 |
| L14_debug_info | L14 调试信息 | 20 | 0 | 15 | 5 | 50 | 🔶 |
| L15_build_tags | L15 构建标签与多平台 | 20 | 0 | 20 | 0 | 50 | 🔶 |
| L16_template_interp | L16 模板/解释器/元编程 | 20 | 4 | 10 | 6 | 50 | 🔶 |
| L17_stackmap | L17 栈映射 | 20 | 9 | 8 | 3 | 50 | 🔶 |
| L18_gc_lifecycle_wb | L18 GC 生命周期与写屏障 | 20 | 5 | 12 | 3 | 50 | 🔶 |
| L19_netpoll_timer_signal | L19 netpoll/定时器/信号 | 20 | 4 | 11 | 5 | 50 | 🔶 |
| **合计** | | **380** | | | | **950** | |

## L01_generics — L1 泛型（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l01_box_pointer_gc | P0 | generics | L01, generics, pointer-field, instantiation, gc, write-barrier | 3 |
| l01_constraint_methods | P0 | generics | L01, generics, type-constraint, methods, comparable, union, pointer-receiver, gc, UAF_SEED | 3 |
| l01_constraint_union | P0 | generics | L01, generics, type-constraint, union, tilde, pointer-payload, gc | 3 |
| l01_error_result_union | P1 | generics | L01, generics, error-union, result-type, errors.Is, errors.As, errors.Join, multi-type-params, gc | 4 |
| l01_generic_comparator | P1 | generics | L01, generics, comparator, stable-sort, container/heap, multi-key, pointer-elements, gc, UAF_SEED | 4 |
| l01_generic_error | P1 | generics | L01, generics, generic-error, error-chain, errors.Is, errors.As, errors.Join, gc | 3 |
| l01_generic_graph | P2 | generics | L01, generics, graph, BFS, DFS, topo-sort, cycle-detection, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_interface | P0 | generics | L01, generics, generic-interface, interface-embedding, delegation, method-value, pointer-identity, gc | 3 |
| l01_generic_map_cache | P1 | generics | L01, generics, map-container, LRU, eviction, hit-miss, pointer-identity, gc, UAF_SEED | 3 |
| l01_generic_matrix | P2 | generics | L01, generics, matrix, numeric-constraint, transpose, multiply, algebra-identity, gc, TORTURE_ROUNDS, UAF_SEED | 2 |
| l01_generic_methods | P0 | generics | L01, generics, methods, generic-interface, embedding, dispatch, gc | 3 |
| l01_generic_object_pool | P2 | generics | L01, generics, object-pool, sync.Mutex, reset, reuse, pointer-identity, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_pipeline | P2 | generics | L01, generics, pipeline, batching, channel, ordering, fail-fast, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_queue_stack | P0 | generics | L01, generics, queue, stack, ring-buffer, callback, pointer-identity, gc | 3 |
| l01_generic_search | P2 | generics | L01, generics, sort.Slice, sort.Search, binary-search, pointer-elements, gc, TORTURE_ROUNDS, UAF_SEED | 3 |
| l01_generic_sync_container | P2 | generics | L01, generics, sync.RWMutex, concurrent-container, pointer-values, gc, TORTURE_ROUNDS | 3 |
| l01_generic_variadic | P0 | generics | L01, generics, variadic, slice-expansion, pointer-elements, gc | 3 |
| l01_instantiation_reuse | P1 | generics | L01, generics, instantiation-identity, type-identity, reflect, gc | 3 |
| l01_nested_generics | P0 | generics | L01, generics, nested-instantiation, pointer-chain, multi-type-params, gc | 3 |
| l01_slice_map_generic | P1 | generics | L01, generics, slice-growth, map-container, pointer-elements, gc | 3 |

## L02_concurrency — L2 高并发（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l02_atomic_map_swap | P2 |  | L02, atomic-counter, map-swap, atomic.Pointer, immutable-snapshot, generation, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_atomic_pointer_config | P1 |  | L02, atomic.Value, hot-reload, immutable-config, pointer, generation, gc | 2 |
| l02_bounded_queue_backpressure | P2 |  | L02, bounded-queue, backpressure, sync.Cond, producer-consumer, gc, TORTURE_ROUNDS | 3 |
| l02_chan_close_drain | P0 |  | L02, channel-close, drain, recover, double-close, closed-select, gc | 3 |
| l02_chan_interface_message | P1 |  | L02, channel-of-interface, type-switch, nil-interface, dynamic-type, pointer-payload, gc | 3 |
| l02_chan_ring_producers_consumers | P2 |  | L02, producer-consumer, bounded-channel, pointer-message, sync.WaitGroup, gc, check-sum, UAF_SEED, TORTURE_ROUNDS | 2 |
| l02_concurrent_counter_churn | P2 |  | L02, atomic, counter, CAS-max, concurrent-alloc, interior-pointer, gc, TORTURE_ROUNDS | 3 |
| l02_context_cancel_fanout | P1 |  | L02, context, WithCancel, fanout, broadcast, shutdown, gc | 2 |
| l02_context_deadline_tree | P0 |  | L02, context, deadline-tree, WithCancel, WithDeadline, propagation, DeadlineExceeded, gc | 3 |
| l02_ctx_timeout_prodcons | P1 |  | L02, context, WithCancel, WithDeadline, DeadlineExceeded, producer-consumer, residual-drain, gc | 3 |
| l02_double_close_recover | P0 |  | L02, double-close, recover, idempotent-shutdown, sync.Once, nil-channel, closed-send, gc | 3 |
| l02_fanin_fanout_two_level | P2 |  | L02, fan-in, fan-out, two-level, WaitGroup, close-cascade, checksum, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_once_registry_init | P1 |  | L02, sync.Once, nested-once, registry, duplicate-registration, concurrent-lookup, panic-once, gc | 3 |
| l02_rwmutex_shared_map | P1 |  | L02, RWMutex, shared-map, pointer-record, snapshot, gc | 3 |
| l02_rwmutex_struct_slice | P1 |  | L02, RWMutex, struct-slice, snapshot-copy, in-place-update, gc, UAF_SEED | 3 |
| l02_select_multiplex_close | P0 |  | L02, select, channel-close, drain, multiplex, pointer-message, gc | 2 |
| l02_sync_once_pool_init | P1 |  | L02, sync.Once, sync.Pool, concurrent-init, reuse, reset, gc | 3 |
| l02_syncmap_pointer_cache | P2 |  | L02, sync.Map, pointer-cache, LoadOrStore, Range, generation, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l02_waitgroup_pipeline | P1 |  | L02, WaitGroup, pipeline, channel, pointer-payload, stage-close, gc | 2 |
| l02_worker_pool_quit | P0 |  | L02, worker-pool, quit-channel, WaitGroup, peak-concurrency, barrier, gc | 3 |

## L03_async_preempt — L3 异步抢占（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l03_atomic_spin_loop | P0 |  | L03, async-preempt, atomic, spin-loop, gc, preemption | 2 |
| l03_big_stack_local | P2 |  | L03, async-preempt, large-stack-frame, stack-growth, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_branch_switch_holdptr | P0 |  | L03, async-preempt, switch-branch, pointer-liveness, gc, UAF_SEED | 2 |
| l03_build_loop_gc | P2 |  | L03, async-preempt, map-build, slice-build, pointer-chain, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_call_each_round_stack | P2 |  | L03, async-preempt, call-per-round, stack-growth, stack-shrink, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_chan_every_n | P1 |  | L03, async-preempt, channel-pingpong, every-n, message-pointer, gc, UAF_SEED | 2 |
| l03_copy_loop_memmove | P2 |  | L03, async-preempt, memmove, copy-loop, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_divmod_index_loop | P0 |  | L03, async-preempt, division, modulo, computed-index, gc, UAF_SEED | 2 |
| l03_iface_call_loop | P1 |  | L03, async-preempt, interface-dispatch, itab, pointer-holder, gc, UAF_SEED | 2 |
| l03_long_slice_iteration | P1 |  | L03, async-preempt, slice-iteration, pointer-array, gc, stack-map | 2 |
| l03_loop_defer_accum | P1 |  | L03, async-preempt, defer-in-loop, defer-record, pointer-capture, gc, UAF_SEED | 2 |
| l03_loop_with_calls_stack | P1 |  | L03, async-preempt, stack-growth, recursion, gc, stack-copy | 2 |
| l03_map_range_gc | P1 |  | L03, async-preempt, map-range, pointer-chase, gc, UAF_SEED | 2 |
| l03_nested_goroutines | P2 |  | L03, async-preempt, nested-goroutines, tight-loop, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l03_nested_loops_pointers | P1 |  | L03, async-preempt, nested-loop, local-pointer, gc, UAF_SEED | 2 |
| l03_select_worker_preempt | P1 |  | L03, async-preempt, select, worker-loop, tight-loop, gc, UAF_SEED | 2 |
| l03_ticker_tight_loop | P1 |  | L03, async-preempt, ticker, tight-loop, gc, timer | 2 |
| l03_tight_loop_no_calls | P0 |  | L03, async-preempt, tight-loop, safepoint, stack-map, gc | 2 |
| l03_tight_loop_rare_gc | P0 |  | L03, async-preempt, tight-loop, noinline, periodic-gc, safepoint | 2 |
| l03_unsafe_ptr_loop | P1 |  | L03, async-preempt, unsafe-pointer, unsafe-add, pointer-liveness, gc, UAF_SEED | 2 |

## L04_scheduler_stack — L4 协程调度/栈（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l04_big_frame_repeat | P1 |  | L04, morestack, stack-copy, big-frame, stackmap, gc, UAF_SEED | 2 |
| l04_blocked_chan_wakeup | P1 |  | L04, channel, close-wakeup, select, sudog, gc, UAF_SEED | 2 |
| l04_bounded_pool | P2 |  | L04, bounded-pool, semaphore, backpressure, worker-pool, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_cross_goroutine_handoff | P1 |  | L04, channel, handoff, ownership, producer-consumer, gc, UAF_SEED | 3 |
| l04_deep_recursion_stack | P1 |  | L04, morestack, stack-copy, recursion, gc, UAF_SEED | 2 |
| l04_goroutine_churn | P1 |  | L04, GPM, goroutine, waitgroup, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_goroutine_leak_numgor | P1 |  | L04, goroutine-leak, NumGoroutine, runtime.Stack, WaitGroup, gc, UAF_SEED | 2 |
| l04_goroutine_local_captures | P0 |  | L04, closure, capture, goroutine-local, gosched, gc, UAF_SEED | 2 |
| l04_goroutine_panic_recover | P0 |  | L04, panic, recover, defer, stack-unwind, pointer, gc, UAF_SEED | 2 |
| l04_goroutine_stack_growth | P1 |  | L04, morestack, stack-growth, stack-copy, goroutine, gc, UAF_SEED | 2 |
| l04_gosched_fairness | P1 |  | L04, Gosched, GPM, fairness, pointer, gc, UAF_SEED | 2 |
| l04_gosched_spin | P1 |  | L04, Gosched, spin, preemption, GPM, gc, UAF_SEED | 2 |
| l04_many_goroutines_big_frames | P1 |  | L04, morestack, stack-copy, big-frame, many-goroutines, gc, UAF_SEED | 3 |
| l04_mutex_blocked_gc | P1 |  | L04, mutex, rwmutex, blocking, sema, gc, UAF_SEED | 3 |
| l04_select_shutdown | P2 |  | L04, select, shutdown, worker-pool, goroutine, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_sleep_wake_pointer | P1 |  | L04, sleep-wake, timer, pointer, gc, UAF_SEED | 2 |
| l04_stack_shrink_cycles | P1 |  | L04, stack-shrink, morestack, recursion, gc, UAF_SEED | 2 |
| l04_timer_fairness | P2 |  | L04, timer, fairness, round-robin, sync.Cond, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l04_timer_heap_pointers | P2 |  | L04, timer, timer-heap, closure, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l04_waitgroup_close | P2 |  | L04, WaitGroup, close, producer-consumer, channel, gc, UAF_SEED, TORTURE_ROUNDS | 3 |

## L05_alloc_gc — L5 大量内存分配（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l05_allocator_reset_pool | P1 |  | L05, sync.Pool, allocator-reset, uvarint, crc32, gc | 1 |
| l05_append_growth_gc | P0 |  | L05, append, slice-growth, copy, gc | 1 |
| l05_builder_vs_buffer | P1 |  | L05, strings.Builder, bytes.Buffer, reset-reuse, snapshot, gc | 2 |
| l05_closure_escape_alloc | P1 |  | L05, closure, escape-analysis, captured-slice, pointer, gc | 1 |
| l05_concurrent_compress_gc | P1 |  | L05, concurrency, compress/gzip, alloc-churn, gc | 1 |
| l05_graph_batch_release | P2 |  | L05, object-graph, batch-release, rebuild, liveness, gc, torture | 1 |
| l05_gzip_reset_churn | P1 |  | L05, compress/gzip, Writer.Reset, buffer-reuse, gc | 1 |
| l05_huge_objects_rounds | P2 |  | L05, huge-object, large-allocation, alloc-free-rounds, crc32, gc, torture | 2 |
| l05_iface_boxing_pressure | P1 |  | L05, interface-boxing, any, type-switch, allocation, gc | 1 |
| l05_iobuf_size_classes | P0 |  | L05, sync.Pool, size-class, large-object, gc | 1 |
| l05_map_bytes_churn | P2 |  | L05, map-churn, map[string][]byte, liveness, gc, torture | 1 |
| l05_map_delete_reinsert | P2 |  | L05, map-churn, delete, reinsert, liveness, gc, torture | 1 |
| l05_pool_churn_gc | P0 |  | L05, sync.Pool, slice-reuse, gc | 1 |
| l05_pool_struct_slice | P1 |  | L05, sync.Pool, struct-with-slice, spans, pointer-fields, gc | 1 |
| l05_prealloc_cap_append | P0 |  | L05, make-cap, append-growth, backing-array, slice-reuse, gc | 1 |
| l05_scratch_reuse_blocks | P1 |  | L05, scratch-reuse, histogram, slice-reuse, gc | 1 |
| l05_skiplist_big_values | P2 |  | L05, skiplist, concurrency, large-object, gc, torture | 1 |
| l05_stream_large_value_pooled | P1 |  | L05, sync.Pool, large-value, streaming, crc32, gc | 1 |
| l05_struct_ptr_slice_copy | P1 |  | L05, slice-of-struct, pointer-fields, append-growth, copy, gc | 1 |
| l05_value_delete_rewrite | P1 |  | L05, large-value, delete, rewrite, liveness, gc | 1 |

## L06_reflect_iface — L6 反射与接口（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l06_boxed_struct_pointers | P0 |  | L06, interface-boxing, struct-value, pointer-fields, GC | 2 |
| l06_deepequal_ptr_graph | P2 | reflect | L06, reflect, DeepEqual, pointer-graph, cycle, func, NaN, GC, torture | 3 |
| l06_empty_iface_containers | P2 |  | L06, empty-interface, nested-containers, type-switch, GC, torture | 2 |
| l06_error_assert_matrix | P1 | reflect | L06, error, errors.As, errors.Is, errors.Join, unwrap-forest, typed-nil, GC | 3 |
| l06_error_wrap_chain | P1 |  | L06, error-chain, errors.Is, errors.As, errors.Join, GC | 2 |
| l06_iface_convert_matrix | P2 | reflect | L06, interface-conversion, type-assertion, Implements, ConvertibleTo, GC, torture | 4 |
| l06_iface_implements_runtime | P1 | reflect | L06, reflect, Implements, AssignableTo, ConvertibleTo, io-interfaces, GC | 3 |
| l06_interface_embedding_chain | P1 |  | L06, interface-embedding, wrapper-chain, method-set, itab, GC | 2 |
| l06_method_value_iface | P1 |  | L06, method-value, method-expression, interface-dispatch, method-set, GC | 2 |
| l06_methodset_value_ptr | P0 | reflect | L06, method-set, value-receiver, pointer-receiver, method-value, embedding, GC | 3 |
| l06_reflect_chan_func | P1 | reflect | L06, reflect, chan, select, MakeFunc, variadic, closure, GC | 4 |
| l06_reflect_deep_copy | P2 | reflect | L06, reflect, deep-copy, shallow-copy, pointer-graph, GC, torture | 2 |
| l06_reflect_grow_slice_map | P2 | reflect | L06, reflect, slice-growth, map-growth, Append, SetMapIndex, GC, torture | 3 |
| l06_reflect_new_set_struct | P1 | reflect | L06, reflect, reflect.New, Set, MakeSlice, MakeMap, struct-copy, GC | 3 |
| l06_reflect_slice_map | P1 | reflect | L06, reflect, slice, map, pointer-elements, GC | 2 |
| l06_reflect_struct_fields | P1 | reflect | L06, reflect, struct-field, pointer-field, Set, GC | 2 |
| l06_reflect_unexported_iface | P1 | reflect | L06, reflect, unexported-field, embedded-struct, interface-field, GC | 3 |
| l06_type_assert_loop | P0 |  | L06, type-assertion, comma-ok, nil-interface, panics, GC | 2 |
| l06_type_switch_many | P1 |  | L06, type-switch, interface-boxing, itab, GC | 2 |
| l06_typed_nil_empty_iface | P0 | reflect | L06, typed-nil, empty-interface, reflect, DeepEqual, map-key, panic, GC | 3 |

## L07_unsafe_zerocopy — L7 unsafe 与零拷贝（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l07_array_elem_gep_arith | P1 |  | L07, unsafe, pointer-arithmetic, GEP, arrays, interior-pointer | 2 |
| l07_atomic_pointer_swap_chain | P2 |  | L07, atomic, pointer-swap, CAS, concurrent, write-barrier | 3 |
| l07_byte_pool_alias_copy | P1 |  | L07, unsafe, byte-pool, alias, copy-semantics | 2 |
| l07_byte_to_string_lifetime | P1 |  | L07, unsafe, string, lifetime, alias-vs-copy | 3 |
| l07_global_pointer_table | P1 |  | L07, unsafe, global-root, pointer-table, map, slice, GC | 3 |
| l07_header_zero_copy_parse | P2 |  | L07, unsafe, header-parser, zero-copy, slice-offset | 2 |
| l07_interior_pointer_liveness | P0 |  | L07, unsafe, interior-pointer, slice-view, liveness | 2 |
| l07_pointer_array_slots | P2 |  | L07, unsafe, pointer-array, atomic, pointer-arithmetic, nested-pointer | 2 |
| l07_pointer_slot_arena | P1 |  | L07, unsafe, pointer-slots, arena, reuse, generation, write-barrier | 3 |
| l07_query_zerocopy_parse_ro | P2 |  | L07, zero-copy, query-parser, percent-decode, input-immutable | 3 |
| l07_readonly_string_header_view | P0 |  | L07, unsafe, string-header, read-only, KeepAlive, FNV | 3 |
| l07_slab_reuse_alias_regions | P2 |  | L07, unsafe, buffer-reuse, slab, alias, snapshot | 3 |
| l07_slice_header_from_heap | P0 |  | L07, unsafe, unsafe.Slice, slice-cap, append, write-barrier | 2 |
| l07_slice_header_lencap_ops | P0 |  | L07, unsafe, slice-header, len-cap, three-index, reslice | 3 |
| l07_slice_header_pointer_math | P0 |  | L07, unsafe, pointer-arithmetic, slice-view, offset | 1 |
| l07_string_bytes_boundary_keepalive | P1 |  | L07, string, bytes, lifetime, KeepAlive, alias-vs-copy | 3 |
| l07_string_bytes_zero_copy | P0 |  | L07, unsafe, string, bytes, alias | 1 |
| l07_struct_padding_alignment | P0 |  | L07, unsafe, struct-layout, padding, alignment, Offsetof | 3 |
| l07_unsafe_global_atomic | P1 |  | L07, unsafe, atomic, global, liveness | 2 |
| l07_unsafe_struct_field_set | P1 |  | L07, unsafe, struct-field, offset, write-barrier, zeroing | 2 |

## L08_asm_simd — L8 汇编与 SIMD（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l08_asm_abi0_amd64 | P1 | asm, amd64 | L08, asm, ABI0, amd64 | 2 |
| l08_asm_bitops | P1 | asm, amd64 | L08, asm, amd64, popcount, rotate, SWAR | 2 |
| l08_asm_clamp_i64 | P1 | asm, amd64 | L08, asm, amd64, clamp, cmov | 2 |
| l08_asm_copy_loop | P1 | asm, amd64 | L08, asm, amd64, SSE2, copy, sentinel | 2 |
| l08_asm_count_zero | P1 | asm, amd64 | L08, asm, amd64, SSE2, popcount, zero-bytes | 2 |
| l08_asm_dot_i16 | P1 | asm, amd64 | L08, asm, amd64, SSE2, dot-product | 2 |
| l08_asm_first_nonzero | P1 | asm, amd64 | L08, asm, amd64, SSE2, scan | 2 |
| l08_asm_hash_loop | P1 | asm, amd64 | L08, asm, loop, byte-slice, FNV | 1 |
| l08_asm_maxmin | P1 | asm, amd64 | L08, asm, amd64, max-min, branch | 2 |
| l08_asm_memchr | P1 | asm, amd64 | L08, asm, amd64, SSE2, memchr, byte-scan | 2 |
| l08_asm_memeq | P1 | asm, amd64 | L08, asm, amd64, SSE2, memequal, bytes-equal | 2 |
| l08_asm_memset_fill | P1 | asm, amd64 | L08, asm, amd64, memset, fill, sentinel | 2 |
| l08_asm_mul128 | P1 | asm, amd64 | L08, asm, amd64, mul, 128-bit | 3 |
| l08_asm_ptr_array | P1 | asm, amd64 | L08, asm, amd64, pointer-array, struct-field, GC | 2 |
| l08_asm_reverse_bytes | P1 | asm, amd64 | L08, asm, amd64, reverse, in-place | 2 |
| l08_asm_shift_rot | P1 | asm, amd64 | L08, asm, amd64, shift, rotate, masking | 2 |
| l08_asm_slice_sum | P1 | asm, amd64 | L08, asm, amd64, slice, sum, loop-unroll | 2 |
| l08_asm_strcmp_lex | P1 | asm, amd64 | L08, asm, amd64, strcmp, byte-compare | 2 |
| l08_asm_struct_fill | P1 | asm, amd64 | L08, asm, amd64, struct, pointer-field, GC | 3 |
| l08_asm_sum_u32 | P1 | asm, amd64 | L08, asm, amd64, uint32, sum, unroll | 2 |

## L09_cgo — L9 cgo（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l09_cgo_callback | P1 | cgo | L09, cgo, callback, export, function-pointer, GC | 2 |
| l09_cgo_const_string | P1 | cgo | L09, cgo, const-char*, GoString, copy | 2 |
| l09_cgo_cstring | P1 | cgo | L09, cgo, CString, GoString, CBytes, NUL | 2 |
| l09_cgo_errno | P1 | cgo | L09, cgo, errno, syscall, ENOENT | 2 |
| l09_cgo_fnptr_table | P1 | cgo | L09, cgo, function-pointer, dispatch | 2 |
| l09_cgo_go_slice_to_c | P1 | cgo | L09, cgo, go-slice, sentinel, pointer-rules | 2 |
| l09_cgo_libm_math | P1 | cgo | L09, cgo, libm, float, math | 2 |
| l09_cgo_malloc_and_slice | P1 | cgo | L09, cgo, malloc, Go-pointer-to-C | 1 |
| l09_cgo_malloc_gc_rounds | P1 | cgo | L09, cgo, malloc, calloc, GC-rounds, unsafe.Pointer | 2 |
| l09_cgo_memmove_overlap | P1 | cgo | L09, cgo, memmove, overlap, sentinel | 2 |
| l09_cgo_nested_struct | P1 | cgo | L09, cgo, nested-struct, layout | 2 |
| l09_cgo_pure_calls | P1 | cgo | L09, cgo, C-call, pointer-liveness | 1 |
| l09_cgo_qsort_cb | P1 | cgo | L09, cgo, qsort, callback, export, GC | 3 |
| l09_cgo_return_ptr_len | P1 | cgo | L09, cgo, ptr-len, unsafe.Slice, malloc, NULL | 2 |
| l09_cgo_struct | P1 | cgo | L09, cgo, struct, sizeof, offsetof, layout | 2 |
| l09_cgo_struct_array | P1 | cgo | L09, cgo, struct-array, unsafe.Slice, GC | 2 |
| l09_cgo_union64 | P1 | cgo | L09, cgo, union, little-endian, punning | 2 |
| l09_cgo_union_bitfield | P1 | cgo | L09, cgo, union, bitfield, layout, little-endian | 2 |
| l09_cgo_userdata_cb | P1 | cgo | L09, cgo, userdata, callback, export, GC | 3 |
| l09_cgo_write_go_array | P1 | cgo | L09, cgo, go-array, length-return, LEB128, sentinel | 2 |

## L10_codegen — L10 代码生成（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l10_codegen_parse_check | P1 | goast | L10, L16, goast, text/template, go-parser, go-types, AST, UAF_SEED | 3 |
| l10_deepcopy_cycle | P1 |  | L10, deepcopy, cycle, aliasing, identity-map, gc, UAF_SEED | 3 |
| l10_deepcopy_generated | P1 |  | L10, deepcopy, nested-struct, slice, map, aliasing, gc, UAF_SEED | 2 |
| l10_dispatch_switch | P2 |  | L10, L11, dispatch-switch, 1024-opcodes, default-branch, jump-table, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_enum_stringer | P0 |  | L10, L11, enum, stringer, index-table, prefix-trim, text-marshal, gc, UAF_SEED | 4 |
| l10_enum_switch | P0 |  | L10, L11, enum, stringer, switch-table, 500-values, gc, UAF_SEED | 2 |
| l10_generated_getters | P1 |  | L10, L06, opaque-accessors, presence, nil-receiver, reset, gc, UAF_SEED | 2 |
| l10_init_registry | P1 |  | L10, L12, init, registry, duplicate, freeze, sync.Once, gc | 3 |
| l10_large_const_table | P2 |  | L10, L11, const-table, compile-time-folding, binary-search, GC, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_large_methodset | P2 |  | L10, L06, method-set, interface-dispatch, method-value, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_marshal_buffer | P1 |  | L10, marshal, append-buffer, packed-repeated, size, gc, UAF_SEED | 3 |
| l10_oneof_roundtrip | P0 |  | L10, L06, oneof, marshal, roundtrip, typed-nil, last-one-wins, unknown-field, gc, UAF_SEED | 3 |
| l10_oneof_union | P0 |  | L10, L06, oneof, type-switch, typed-nil, last-one-wins, gc, UAF_SEED | 2 |
| l10_optional_getters | P1 |  | L10, L06, optional, getter, presence, default-table, nil-receiver, method-value, gc, UAF_SEED | 3 |
| l10_proto_struct_switch | P1 |  | L10, generated-struct, field-switch, protowire, zigzag, unknown-field, gc, UAF_SEED | 3 |
| l10_registry_dup_detect | P2 |  | L10, L12, descriptor-registry, duplicate-detect, atomic-commit, conflict-sort, gc, UAF_SEED, TORTURE_ROUNDS | 4 |
| l10_service_desc_table | P1 |  | L10, L12, service-desc, method-table, duplicate-detect, atomic-register, closure, gc, UAF_SEED | 3 |
| l10_service_stub | P2 |  | L10, L06, rpc-stub, codec-registry, error-path, max-size, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l10_template_format_ast | P1 | goast | L10, L16, goast, text-template, go-format, go-parser, AST, directive, GC, UAF_SEED | 3 |
| l10_thrift_binary | P2 |  | L10, L13, thrift-binary, skip, nested-struct, length-table, GC, UAF_SEED, TORTURE_ROUNDS | 3 |

## L11_compiler_opts — L11 编译期优化（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l11_bce_multidim | P2 |  | bounds-check-elimination, multi-dimensional, matrix, jagged, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_bounds_check | P1 |  | bounds-check-elimination, panic-recover, slice-bounds, loop-hoisting, gc, UAF_SEED | 3 |
| l11_closure_map_escape | P1 |  | escape-analysis, closure, map, loop-variable, gc, UAF_SEED | 3 |
| l11_const_divmod | P0 |  | const-fold, division, modulo, magic-number, signed-truncation, wrapping, panic-recover, gc, UAF_SEED | 3 |
| l11_const_fold | P0 |  | const-fold, bitops, shifts, wrapping, constant-string, constant-array, gc | 3 |
| l11_copy_elision | P1 |  | struct-copy, pointer-field, aliasing, slice-copy, array-copy, gc | 2 |
| l11_dce_side_effects | P0 |  | dce, side-effect, dead-branch, panic-recover, allocation, gc | 2 |
| l11_dead_store | P1 |  | dead-store-elimination, dce, aliasing, side-effect, gc, UAF_SEED | 2 |
| l11_devirtualize | P1 |  | devirtualize, interface-dispatch, itab, method-value, type-switch, typed-nil, gc | 3 |
| l11_escape_analysis | P1 |  | escape-analysis, closure, interface-boxing, heap, gc, UAF_SEED | 3 |
| l11_inline_equivalence | P0 |  | inline, noinline, go:noinline, pointer-after-gc, alias-in-slice, gc | 2 |
| l11_itab_multi_impl | P1 |  | devirtualize, interface-dispatch, itab, type-switch, method-value, generics, gc, UAF_SEED | 3 |
| l11_nil_checks | P0 |  | nil-pointer, nil-receiver, typed-nil, nil-map, nil-slice, nil-chan, gc | 2 |
| l11_nilcheck_elim | P0 |  | nil-check-elimination, repeated-deref, pointer-chain, panic-recover, gc, UAF_SEED | 3 |
| l11_open_coded_defer | P0 |  | open-coded-defer, defer-order, recover, named-result, stack-growth, gc, UAF_SEED | 3 |
| l11_recursive_inline | P1 |  | inline, recursion, mutual-recursion, stack-growth, panic-recover, gc, UAF_SEED | 3 |
| l11_slice_cap_bounds | P2 |  | slice-cap, three-index-slice, append, capacity-sentinel, bounds-check, panic-recover, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_string_compare | P2 |  | string-compare, memequal, lexicographic, sort, map-key, unicode, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l11_string_concat | P2 |  | string-concat, strings.Builder, byte-append, rune-count, unicode, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l11_switch_opt | P2 |  | switch, jump-table, if-chain, string-switch, dispatch, gc, UAF_SEED, TORTURE_ROUNDS | 3 |

## L12_init_link — L12 初始化与链接（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l12_big_global_array | P2 |  | L12, global-array, large-initializer, pointer-fields, write-barrier, gc, TORTURE_ROUNDS | 3 |
| l12_blank_import_register | P1 |  | L12, blank-identifier, package-init, registry, side-effect, concurrency, gc | 2 |
| l12_blank_import_sideeffect | P1 |  | L12, blank-import, package-init, side-effect, image/png, net/http/pprof, registry, gc | 2 |
| l12_const_methodset | P0 |  | L12, constants, iota, method-set, value-receiver, pointer-receiver, interface, itab, gc | 3 |
| l12_crossfile_iface_impl | P2 |  | L12, interface, cross-file-methods, method-set, embedding, typed-nil, itab, gc, TORTURE_ROUNDS | 3 |
| l12_dup_register_detect | P1 |  | L12, duplicate-detection, registry, panic, error, case-insensitive, atomicity, gc | 3 |
| l12_func_init_chain | P0 |  | L12, package-init, var-init, function-call, init-order, side-effect, gc | 3 |
| l12_global_once_matrix | P2 |  | L12, sync.Once, lazy-init, panic, concurrency, gc, TORTURE_ROUNDS | 3 |
| l12_global_ptr_tables | P1 |  | L12, global-table, map, slice, pointer-identity, cross-reference, gc, concurrency | 3 |
| l12_global_struct_gc | P1 |  | L12, global-struct, gc, pointers, map, slice, interface, self-reference | 2 |
| l12_init_error_path | P1 |  | L12, init-error, recover, fallback, deterministic, multi-file, gc | 2 |
| l12_init_runs_once | P0 |  | L12, init-order, multi-file, runs-once, gc | 2 |
| l12_iota_consts | P0 |  | L12, iota, typed-constants, untyped-constants, shifts, constant-folding, gc | 2 |
| l12_large_initializer | P2 |  | L12, large-initializer, gcprog, static-data, map, pointers, gc, TORTURE_ROUNDS | 2 |
| l12_module_init_order | P1 | pkgbuild | L12, module, multi-package, package-init, init-order, var-init, cross-package, gc | 3 |
| l12_multi_file_methods | P1 |  | L12, multi-file, methods, interface, embedding, method-set, dispatch, gc | 2 |
| l12_register_sequence | P1 |  | L12, registry, registration-order, sequence, stable-sort, tombstone, gc, concurrency | 3 |
| l12_registry_duplicates | P1 |  | L12, registry, duplicate-detection, stable-order, freeze, concurrency, gc | 2 |
| l12_sync_once_init | P2 |  | L12, sync.Once, lazy-init, concurrency, gc, TORTURE_ROUNDS | 2 |
| l12_var_dependency_order | P0 |  | L12, package-init, var-dependency-order, cross-file, gc | 2 |

## L13_runtime_ds — L13 运行时数据结构（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l13_bytes_string_map_key | P1 |  | L13, bytes-to-string, map-key, ownership, binary-key, growth, gc, UAF_SEED | 3 |
| l13_chunkreader_buffered | P0 |  | L13, chunk-reader, fixed-window, compaction, EOF, gc, UAF_SEED | 1 |
| l13_iobuf_capacity_semantics | P0 |  | L13, buffer-pool, size-class, capacity, gc, UAF_SEED | 1 |
| l13_line_reader_long_lines | P0 |  | L13, bufio, readline, long-line, buffer-boundary, gc, UAF_SEED | 1 |
| l13_map_grow_delete_gc | P0 |  | L13, map, growth, delete, gc | 1 |
| l13_map_iterate_mutate | P0 |  | L13, map, iteration, delete-during-range, insert-during-range, gc | 2 |
| l13_map_of_maps | P1 |  | L13, map-of-maps, nested, alias, nil-map, gc, UAF_SEED | 2 |
| l13_map_string_slice_ptr_churn | P2 |  | L13, map, slice-of-pointers, churn, truncate, snapshot, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l13_merge_iterator_4way | P0 |  | L13, merge-iterator, heap, sorted, duplicate-key, gc | 1 |
| l13_overlap_copy | P0 |  | L13, copy, overlap, memmove, pointer-slice, write-barrier, gc, UAF_SEED | 2 |
| l13_page_buffer_grow | P0 |  | L13, page-buffer, slice-growth, bytes.Buffer, truncate, gc | 1 |
| l13_pipeline_var_len | P1 |  | L13, pipeline, varint, batch, slice-growth, gc, UAF_SEED, TORTURE_ROUNDS | 1 |
| l13_prefix_trie_match | P0 |  | L13, trie, prefix-match, map, dedupe, gc, UAF_SEED | 1 |
| l13_ptr_ring_buffer | P0 |  | L13, ring-buffer, wrap, overwrite, grow, pointer, gc, UAF_SEED | 2 |
| l13_slice_filter_inplace | P2 |  | L13, slice, in-place-filter, compaction, zero-tail, dedupe, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l13_slice_of_slices | P1 |  | L13, slice-of-slices, jagged, aliasing, insert-delete, clone, gc, UAF_SEED | 2 |
| l13_small_capacity_roundtrip | P2 |  | L13, flate, small-capacity, append-growth, roundtrip, gc, UAF_SEED, TORTURE_ROUNDS | 1 |
| l13_string_intern_table | P2 |  | L13, string, intern, canonical-pointer, map, purge, gc, UAF_SEED, TORTURE_ROUNDS | 2 |
| l13_string_slice_bytes_gc | P0 |  | L13, string, substring, bytes, builder, gc | 1 |
| l13_utf8_process | P1 |  | L13, utf8, rune, invalid-sequence, map-key, gc, UAF_SEED | 2 |

## L14_debug_info — L14 调试信息（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l14_anon_func_frames | P1 |  | L14, anonymous-function, IIFE, func-literal, traceback, CallersFrames, gc | 5 |
| l14_callers_methods_closures | P1 |  | L14, CallersFrames, method-symbol, closure-symbol, pointer-receiver, gc | 4 |
| l14_callers_skip | P1 |  | L14, runtime.Callers, skip-semantics, CallersFrames, symbol-resolution, gc | 4 |
| l14_closure_funcname | P1 |  | L14, closure-symbol, funcval, reflect, traceback, gc | 5 |
| l14_closure_lineinfo | P1 |  | L14, closure, line-info, runtime.Caller, debug.Stack, gc | 3 |
| l14_concurrent_panic_recover | P2 |  | L14, concurrent, panic, recover, debug.Stack, goroutine, TORTURE_ROUNDS, gc | 3 |
| l14_debug_stack_large_buf | P2 |  | L14, debug.Stack, runtime.Stack, large-buffer, truncation, goroutine-dump, TORTURE_ROUNDS, gc | 3 |
| l14_deep_panic_traceback | P1 |  | L14, traceback, deep-recursion, goroutine, debug.Stack, gc | 3 |
| l14_funcptr_frames | P2 |  | L14, func-pointer, method-value, reflect-funcval, traceback, TORTURE_ROUNDS, gc | 5 |
| l14_gc_panic_stack | P1 |  | L14, gc, panic-stack, traceback, clobberfree, line-info | 3 |
| l14_goroutine_dump | P1 |  | L14, goroutine-dump, stack-unwind, blocked | 1 |
| l14_goroutine_states_dump | P2 |  | L14, runtime.Stack, goroutine-state, chan-send, select, waitgroup, cond, TORTURE_ROUNDS, gc | 5 |
| l14_inline_frames | P1 |  | L14, inline-frame, traceback, CallersFrames | 1 |
| l14_loop_lineinfo | P2 |  | L14, line-info, loop, runtime.Caller, traceback, TORTURE_ROUNDS, gc | 5 |
| l14_method_chain_traceback | P1 |  | L14, traceback, method-symbol, pointer-receiver, embedded-method, debug.Stack, gc | 5 |
| l14_multi_recover | P1 |  | L14, multi-recover, defer, panic, debug.Stack, closure-symbol, gc | 4 |
| l14_panic_in_defer | P1 |  | L14, panic-in-defer, panic-replacement, recover, debug.Stack, gc | 4 |
| l14_stack_multi_state | P1 |  | L14, runtime.Stack, goroutine-state, chan-receive, select, sleep, mutex, gc | 5 |
| l14_traceback_frames | P1 |  | L14, traceback, debug.Stack, runtime.Callers | 1 |
| l14_wrapped_error_panic | P1 |  | L14, wrapped-error, errors.Is, errors.As, panic, traceback, gc | 5 |

## L15_build_tags — L15 构建标签与多平台（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l15_arch_mutex | P1 | pkgbuild | L15, build-tag, GOARCH, mutual-exclusion, init, file-selection, gc | 5 |
| l15_build_expr_nested | P1 | pkgbuild | L15, build-tag, AND, OR, NOT, parentheses, custom-tags, tautology, gc | 6 |
| l15_buildtag_arch | P1 | pkgbuild | L15, filename-suffix, GOARCH, file-selection | 1 |
| l15_buildtag_custom | P1 | pkgbuild | L15, build-tag, custom-tags | 1 |
| l15_buildtag_os | P1 | pkgbuild | L15, build-tag, GOOS, file-selection | 1 |
| l15_custom_tag_dep | P1 | pkgbuild | L15, build-tag, custom-tags, tag-dependency, init | 4 |
| l15_filename_goos | P1 | pkgbuild | L15, filename-suffix, GOOS, GOARCH, file-selection | 4 |
| l15_goos_init | P1 | pkgbuild | L15, GOOS, init-order, build-tag, file-selection | 4 |
| l15_goos_suffix_tag | P1 | pkgbuild | L15, filename-suffix, GOOS, build-tag, conjunction, custom-tags, gc | 5 |
| l15_module_multipkg | P1 | pkgbuild | L15, module, multi-package, cross-package-link, subpackage-build-tag, init-order, gc | 5 |
| l15_module_var_deps | P1 | pkgbuild | L15, module, multi-package, package-variable, init-order, dependency-chain, gc | 6 |
| l15_multipkg_tags | P1 | pkgbuild | L15, module, multi-package, custom-tags, GOOS, cross-package-link, gc | 5 |
| l15_mutex_files | P1 | pkgbuild | L15, build-tag, mutual-exclusion, file-selection, init | 4 |
| l15_osarch_combo_tag | P1 | pkgbuild | L15, build-tag, GOOS, GOARCH, AND, OR, NOT, file-selection, gc | 5 |
| l15_pkg_init_order | P1 | pkgbuild | L15, module, multi-package, init-order, package-variable, dependency-order, gc | 6 |
| l15_tag_and_or_not | P1 | pkgbuild | L15, build-tag, AND, OR, NOT, GOOS, GOARCH, custom-tags | 5 |
| l15_tag_const_methodset | P1 | pkgbuild | L15, build-tag, constant, method-set, reflect, interface, custom-tags | 5 |
| l15_tag_const_select | P1 | pkgbuild | L15, build-tag, constant, compile-time-array, constant-expression, gc | 5 |
| l15_tag_iface_impl | P1 | pkgbuild | L15, build-tag, interface, implementation-selection, reflection, gc | 5 |
| l15_tag_methodset_subpkg | P1 | pkgbuild | L15, build-tag, method-set, interface, reflection, multi-package, gc | 6 |

## L16_template_interp — L16 模板/解释器/元编程（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l16_ast_rewrite | P2 | goast | L16, goast, go-parser, go-format, ast-rewrite, go-types, GC, TORTURE_ROUNDS | 5 |
| l16_ast_walk | P1 | goast | L16, goast, go-parser, ast-walk, node-count, GC | 2 |
| l16_constant_ops | P2 | goast | L16, goast, go-constant, arbitrary-precision, rational, shifts, conversions, GC, TORTURE_ROUNDS | 4 |
| l16_css_js_url_escape | P2 | templates | L16, templates, html-template, css-escape, js-escape, url-escape, srcset, safe-types, GC, TORTURE_ROUNDS | 4 |
| l16_expr_eval | P1 | goast | L16, goast, go-parser, go-types, constant-folding, expression-eval, GC | 3 |
| l16_funcmap_error | P1 | templates | L16, templates, FuncMap, error-propagation, nested-template, GC | 5 |
| l16_funcmap_pointer | P1 | templates | L16, templates, FuncMap, pointers, variadic, GC | 3 |
| l16_html_escape | P2 | templates | L16, templates, html-template, escaping, contexts, GC, TORTURE_ROUNDS | 3 |
| l16_method_templates | P1 | templates | L16, templates, methods, pointer-receiver, pointer-return, GC | 2 |
| l16_mini_interp | P2 | goast | L16, goast, interpreter, recursion, go-parser, step-limit, GC, TORTURE_ROUNDS | 5 |
| l16_nested_templates | P2 | templates | L16, templates, define, template-nesting, order-independence, GC, TORTURE_ROUNDS | 2 |
| l16_parse_errors | P0 | templates | L16, templates, parse-error, error-line, html-template, escape-context, GC | 5 |
| l16_positions | P0 | goast | L16, goast, go-token, positions, line-directive, GC | 3 |
| l16_range_if_ptr_slice | P1 | templates | L16, templates, text-template, range, nil-pointer, if-with-else, break-continue, GC | 5 |
| l16_reflect_call | P1 | reflect | L16, reflect, reflect-call, pointer-args, variadic, MakeFunc, GC | 3 |
| l16_reflect_method_call | P1 | reflect | L16, reflect, method-value, method-expression, embedding, interface-dispatch, variadic, GC | 5 |
| l16_reflect_struct_tag | P0 | reflect | L16, reflect, struct-tag, json, embedded-field, unexported-field, GC | 4 |
| l16_text_template_fields | P0 | templates | L16, templates, text-template, pointer-fields, GC | 3 |
| l16_types_check | P1 | goast | L16, goast, go-types, type-check, diagnostics, method-set, GC | 2 |
| l16_types_methodset | P1 | goast | L16, goast, go-types, method-set, embedding, interface, MissingMethod, GC | 5 |

## L17_stackmap — L17 栈映射（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l17_addrtaken_local_array | P0 |  | L17, array, address-taken, zero-point | 1 |
| l17_chan_pointer_handoff | P1 |  | L17, stackmap, chan, unbuffered, noinline, GC | 1 |
| l17_chan_struct_ptr | P1 |  | L17, stackmap, channel, struct-by-value, goroutine, interface-boxing, GC, UAF_SEED | 2 |
| l17_closure_defer_combo | P1 |  | L17, stackmap, closure, defer, captured-pointer, per-iteration, GC | 2 |
| l17_defer_iface_method | P0 |  | L17, stackmap, defer, interface-method, itab, deferreturn, GC | 2 |
| l17_defer_panic_unwind | P0 |  | L17, defer, panic, recover, unwind | 1 |
| l17_embed_wrapper_aggregate | P1 |  | L17, stackmap, embedding, wrapper, promoted-method, aggregate-by-value, interface, GC | 2 |
| l17_fca_return_byval | P0 |  | L17, FCA, aggregate, byval | 1 |
| l17_inline_boundary | P0 |  | L17, inline, noinline, aggregate | 1 |
| l17_method_named_defer | P0 |  | L17, stackmap, method, named-return, defer, deferreturn, method-value, GC | 2 |
| l17_method_wrapper_tail | P0 |  | L17, method-value, wrapper, tail-call | 1 |
| l17_morestack_bigframe | P1 |  | L17, morestack, stack-copy, recursion | 1 |
| l17_named_return_defer | P0 |  | L17, defer, named-return, panic | 1 |
| l17_recursive_closure_gc | P2 |  | L17, stackmap, recursive-closure, tree, parent-pointer, GC, TORTURE_ROUNDS, UAF_SEED | 2 |
| l17_select_defer_interleave | P2 |  | L17, stackmap, select, defer, chan-of-struct, per-iteration-capture, GC, TORTURE_ROUNDS, UAF_SEED | 2 |
| l17_slice_struct_byval | P1 |  | L17, stackmap, byval, aggregate, slice-of-struct, string | 1 |
| l17_spill_many_ptr_args | P2 |  | L17, stackmap, spill, register-abi, many-args, pointer-args, GC, TORTURE_ROUNDS, UAF_SEED | 2 |
| l17_stack_ptr_across_call | P0 |  | L17, stackmap, noinline, chan, interface | 1 |
| l17_struct_return_fields | P1 |  | L17, stackmap, aggregate-return, struct-by-value, interface, method, GC | 2 |
| l17_variadic_iface_ptr | P1 |  | L17, stackmap, variadic, interface, []any, method, GC, UAF_SEED | 2 |

## L18_gc_lifecycle_wb — L18 GC 生命周期与写屏障（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l18_big_struct_array_zero | P2 |  | L18, large-object, zeroing, clear, struct-array, write-barrier, TORTURE_ROUNDS | 2 |
| l18_finalizer_reachable | P1 |  | L18, finalizer, liveness, probe | 1 |
| l18_global_ptr_array_churn | P1 |  | L18, write-barrier, global-array, pointer-churn, UAF_SEED | 2 |
| l18_global_roots | P0 |  | L18, globals, gcprog, map, slice, interface | 1 |
| l18_heap_zero_churn | P1 |  | L18, zero-init, FreeOSMemory, large-object | 1 |
| l18_iobuf_pool_reuse_gc | P1 |  | L18, sync.Pool, reuse, size-class, marker, GC | 1 |
| l18_keepalive_boundary | P1 |  | L18, keepalive, liveness, finalizer, gc-boundary | 1 |
| l18_many_finalizers | P2 |  | L18, finalizer, liveness, mass-objects, TORTURE_ROUNDS | 1 |
| l18_map_old_key_new_value_grow | P2 |  | L18, write-barrier, map-growth, old-key, young-value, UAF_SEED, TORTURE_ROUNDS | 1 |
| l18_old_chain_two_hop | P1 |  | L18, write-barrier, old-to-old-to-young, pointer-chain, noinline, gc | 2 |
| l18_old_chan_recv_young | P1 |  | L18, write-barrier, channel, producer-consumer, old-chan, gc | 1 |
| l18_old_interface_hot_swap | P1 |  | L18, write-barrier, interface, itab, hot-swap, noinline, gc | 2 |
| l18_old_slice_bulk_rewrite | P1 |  | L18, write-barrier, old-slice, bulk-rewrite, copy, UAF_SEED | 1 |
| l18_old_to_young_append_chan | P0 |  | L18, write-barrier, append, chan, old-gen | 1 |
| l18_old_to_young_field | P0 |  | L18, write-barrier, old-gen, interface, map, slice | 1 |
| l18_partial_message_recovery | P0 |  | L18, framing, partial-read, buffer-lifecycle, GC | 1 |
| l18_pool_finalizer_combo | P1 |  | L18, sync.Pool, finalizer, liveness, gc | 2 |
| l18_pool_liveness | P0 |  | L18, sync.Pool, external-reference, gc | 1 |
| l18_stream_cancel_reuse | P1 |  | L18, state-machine, cancel, reuse, lifecycle | 1 |
| l18_value_delete_rewrite_gc | P1 |  | L18, write-barrier, large-value, delete, rewrite, UAF_SEED | 1 |

## L19_netpoll_timer_signal — L19 netpoll/定时器/信号（20）

| 用例 | 优先级 | requires | tags | 来源 |
|---|---|---|---|---|
| l19_after_timeout_select | P0 |  | L19, select, time-after, timeout, pointer, gc | 2 |
| l19_afterfunc_reset | P0 |  | L19, afterfunc, timer-reset, callback-state, pointer, gc, UAF_SEED | 3 |
| l19_context_timeout_path | P1 |  | L19, context, with-timeout, cancel, afterfunc, timer, pointer, gc, UAF_SEED | 3 |
| l19_goroutine_block_io | P2 | net | L19, blocking-io, net-pipe, os-pipe, goroutine, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_multi_timer_cancel | P1 |  | L19, timer-cancel, stop, concurrent, afterfunc, pointer, gc, UAF_SEED | 3 |
| l19_netpipe_chain | P1 | net | L19, net-pipe, multi-hop, netpoll, blocking-io, pointer, gc, UAF_SEED | 3 |
| l19_netpipe_payload | P1 | net | L19, net-pipe, netpoll, goroutine, pointer, gc, close, UAF_SEED | 2 |
| l19_os_pipe_bulk | P2 |  | L19, os-pipe, blocking-io, bulk-data, netpoll, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_os_pipe_poll | P1 |  | L19, os-pipe, blocking-io, netpoll, pointer, gc, eof, UAF_SEED | 2 |
| l19_select_default_poll | P0 |  | L19, select-default, nonblocking, channel-polling, pointer, gc, deterministic | 3 |
| l19_signal_ignore_reset | P1 | signals, unix | L19, signal, signal-ignore, signal-reset, sigusr1, sigusr2, pointer, gc, linux-only | 2 |
| l19_signal_notify | P1 | signals, unix | L19, signal, sigusr1, syscall-kill, pointer, gc, linux-only | 1 |
| l19_tcp_loopback | P1 | net | L19, tcp, loopback, netpoll, echo, pointer, gc, UAF_SEED | 2 |
| l19_ticker_stop_reset | P1 |  | L19, ticker, stop, reset, concurrent-stop, pointer, gc, UAF_SEED | 3 |
| l19_ticker_worker | P1 |  | L19, ticker, worker-loop, pointer, gc, stop, UAF_SEED | 2 |
| l19_timer_boundary_gc | P1 |  | L19, afterfunc, timer-heap, liveness, unique-reference, pointer, gc, UAF_SEED | 2 |
| l19_timer_coalesce_cancel | P2 |  | L19, timer-reset, coalescing, batch-cancel, afterfunc, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_timer_heap_many | P2 |  | L19, timer-heap, afterfunc, closure, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |
| l19_timer_stop_reset | P0 |  | L19, timer, stop, reset, pointer, gc, deterministic | 2 |
| l19_udp_loopback | P2 | net | L19, udp, loopback, netpoll, echo, pointer, gc, UAF_SEED, TORTURE_ROUNDS | 3 |

