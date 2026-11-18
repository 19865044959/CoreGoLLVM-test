---
id: l12_register_sequence
dim: L12_init_link
priority: P1
sources:
  - kitex@v0.13.0: pkg/loadbalance/lbcache/hookable_test.go:26 hook 索引 0..4 单调、注销后不复用
  - fiber@v2.52.6: mount_test.go:56 三层 Mount 注册/计数顺序
  - caddy@v2.10.0: modules_test.go:22 全局模块注册表
tags: [L12, registry, registration-order, sequence, stable-sort, tombstone, gc, concurrency]
requires: []
runtime: "<10s"
---

# 场景

三个文件各自的 init 注册一个 handler，跨文件顺序未定义。注册表为每次注册分配单调递增序号，按注册顺序维护 `order` 切片；`byPriority` 用稳定排序生成优先级视图。主程序再显式注册两个 handler，注销一个后继续注册，验证序号不复用、视图稳定、GC 后状态不变。

# 期望行为

1. init 后恰有 3 个 handler，序号为 0/1/2 且与 `order` 下标一致；名字集合为 alpha/beta/gamma（不假设顺序）；
2. 显式注册 delta(prio=1)、epsilon(prio=5) 得到序号 3/4，追加在 `order` 末尾；
3. 优先级视图按 prio 非降序且同 prio 保持序号升序；本例唯一确定为 `beta,delta,gamma,alpha,epsilon`；
4. 注销首个 handler 后：`order` 长度减一、`nextSeq` 不变、不存在同名查找；
5. 注册 zeta(prio=0) 得到序号 5（不复用 0）、追加在末尾，优先级视图首位为 zeta；
6. 注销不存在的名字返回 false 且不改变序号状态；
7. 8 goroutine 并发查找 + GC 期间指针/序号稳定；GC 后 zeta 序号仍为 5、`nextSeq=6`；
8. 最后一行 `ALL PASS l12_register_sequence`。

# 失败判读

- init 后序号不是 0/1/2：注册序号分配错误或 init 重复执行；
- `order` 与 seq 不一致：注册顺序记录错误；
- 注销后出现序号复用/重排：墓碑或 nextSeq 实现错误；
- 同优先级顺序变化：排序不稳定；
- GC 后查找失败：全局 map/slice 指针扫描错误；
- 失败行 `seed=` 可用于复现并发读取起点。

# 参数

- `UAF_SEED`：决定并发只读阶段的起始下标；断言与起点无关。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_register_sequence/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
