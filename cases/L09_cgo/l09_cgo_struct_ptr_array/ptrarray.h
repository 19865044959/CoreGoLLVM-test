#ifndef L09_PTRARRAY_H
#define L09_PTRARRAY_H

#include <stdint.h>

/* C 结构体指针数组：arr[k] 指向独立 malloc 的节点，类似 C 库的
 * 图邻接表 / 句柄列表。整块内存均由 C 拥有，Go 只在调用期内读写。 */
typedef struct node {
	int32_t id;
	int32_t weight;
	int64_t value;
	char tag[8];
} node_t;

node_t **c_nodes_new(int32_t n, int32_t *count);
void c_nodes_free(node_t **nodes, int32_t n);
int64_t c_nodes_sum(node_t **nodes, int32_t n);
void c_node_add_value(node_t *node, int64_t dv);
void c_node_set_weight(node_t *node, int32_t w);

#endif
