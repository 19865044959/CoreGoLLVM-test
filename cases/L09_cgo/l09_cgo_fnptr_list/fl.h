#ifndef L09_FL_H
#define L09_FL_H

#include <stddef.h>
#include <stdint.h>

/* C 侧侵入式链表节点：函数指针与 next 指针都在同一个结构体内。 */
typedef struct fl_node fl_node;
typedef int64_t (*fl_fn)(const fl_node *n, int64_t acc);

struct fl_node {
	int64_t value;
	int32_t kind;
	int32_t pad;
	fl_fn fn;
	fl_node *next;
};

size_t fl_size(void);
size_t fl_off_value(void);
size_t fl_off_kind(void);
size_t fl_off_fn(void);
size_t fl_off_next(void);

/* 分配 n 个节点并串联；失败返回 NULL。value=i*17-40，初始 kind=i%4。 */
fl_node *fl_build(int n);
void fl_free(fl_node *head);
int fl_count(const fl_node *head);
int fl_null_fn(const fl_node *head);
int64_t fl_value_at(const fl_node *head, int idx);
int fl_fn_id(const fl_node *head, int idx);

/* 幂等替换 idx 处节点的函数指针（id 0..3）；越界返回 -1。 */
int fl_set_fn(fl_node *head, int idx, int id);

/* 迭代 / 递归按链序执行 acc = fn(node, acc)。 */
int64_t fl_walk(const fl_node *head, int64_t seed);
int64_t fl_walk_rec(const fl_node *head, int64_t seed);

#endif
