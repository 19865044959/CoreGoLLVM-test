#include "ptrarray.h"

#include <stdlib.h>

node_t **c_nodes_new(int32_t n, int32_t *count) {
	if (n <= 0) {
		*count = 0;
		return NULL;
	}
	node_t **arr = (node_t **)calloc((size_t)n, sizeof(node_t *));
	if (arr == NULL) {
		*count = 0;
		return NULL;
	}
	for (int32_t i = 0; i < n; i++) {
		node_t *p = (node_t *)malloc(sizeof(node_t));
		if (p == NULL) {
			for (int32_t j = 0; j < i; j++) {
				free(arr[j]);
			}
			free(arr);
			*count = 0;
			return NULL;
		}
		p->id = i;
		p->weight = (i * 7) % 13;
		p->value = (int64_t)i * 1000 + 7;
		for (int32_t j = 0; j < 7; j++) {
			p->tag[j] = (char)('a' + (i + j) % 26);
		}
		p->tag[7] = 0;
		arr[i] = p;
	}
	*count = n;
	return arr;
}

void c_nodes_free(node_t **nodes, int32_t n) {
	if (nodes == NULL) {
		return;
	}
	for (int32_t i = 0; i < n; i++) {
		free(nodes[i]);
	}
	free(nodes);
}

int64_t c_nodes_sum(node_t **nodes, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) {
		if (nodes[i] != NULL) {
			s += (int64_t)nodes[i]->id + (int64_t)nodes[i]->weight + nodes[i]->value;
		}
	}
	return s;
}

void c_node_add_value(node_t *node, int64_t dv) {
	if (node != NULL) {
		node->value += dv;
	}
}

void c_node_set_weight(node_t *node, int32_t w) {
	if (node != NULL) {
		node->weight = w;
	}
}
