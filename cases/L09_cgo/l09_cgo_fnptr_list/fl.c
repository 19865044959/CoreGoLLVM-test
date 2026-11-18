#include "fl.h"

#include <stdlib.h>

size_t fl_size(void) { return sizeof(fl_node); }
size_t fl_off_value(void) { return offsetof(fl_node, value); }
size_t fl_off_kind(void) { return offsetof(fl_node, kind); }
size_t fl_off_fn(void) { return offsetof(fl_node, fn); }
size_t fl_off_next(void) { return offsetof(fl_node, next); }

static int64_t fn_add(const fl_node *n, int64_t acc) { return acc + n->value; }
static int64_t fn_sub(const fl_node *n, int64_t acc) { return acc - n->value; }
static int64_t fn_xor(const fl_node *n, int64_t acc) {
	return acc ^ (int64_t)((uint64_t)n->value * UINT64_C(2654435761));
}
static int64_t fn_mulmod(const fl_node *n, int64_t acc) {
	return (acc * 3 + n->value) % INT64_C(1000000007);
}

static fl_fn fn_by_id(int id) {
	switch (id) {
	case 0:
		return fn_add;
	case 1:
		return fn_sub;
	case 2:
		return fn_xor;
	case 3:
		return fn_mulmod;
	default:
		return NULL;
	}
}

fl_node *fl_build(int n) {
	fl_node *head = NULL;
	fl_node *tail = NULL;
	for (int i = 0; i < n; i++) {
		fl_node *p = (fl_node *)calloc(1, sizeof(fl_node));
		if (p == NULL) {
			fl_free(head);
			return NULL;
		}
		p->value = (int64_t)i * 17 - 40;
		p->kind = (int32_t)(i % 4);
		p->fn = fn_by_id(p->kind);
		p->pad = 0;
		p->next = NULL;
		if (tail == NULL)
			head = p;
		else
			tail->next = p;
		tail = p;
	}
	return head;
}

void fl_free(fl_node *head) {
	while (head != NULL) {
		fl_node *next = head->next;
		free(head);
		head = next;
	}
}

int fl_count(const fl_node *head) {
	int n = 0;
	for (; head != NULL; head = head->next)
		n++;
	return n;
}

int fl_null_fn(const fl_node *head) {
	int n = 0;
	for (; head != NULL; head = head->next)
		if (head->fn == NULL)
			n++;
	return n;
}

static const fl_node *fl_at(const fl_node *head, int idx) {
	if (idx < 0)
		return NULL;
	for (int i = 0; head != NULL && i < idx; i++)
		head = head->next;
	return head;
}

int64_t fl_value_at(const fl_node *head, int idx) {
	const fl_node *p = fl_at(head, idx);
	return p == NULL ? INT64_MIN : p->value;
}

int fl_fn_id(const fl_node *head, int idx) {
	const fl_node *p = fl_at(head, idx);
	return p == NULL ? -1 : (int)p->kind;
}

int fl_set_fn(fl_node *head, int idx, int id) {
	fl_node *p = (fl_node *)fl_at(head, idx);
	fl_fn f = fn_by_id(id);
	if (p == NULL || f == NULL)
		return -1;
	p->kind = (int32_t)id;
	p->fn = f;
	return 0;
}

int64_t fl_walk(const fl_node *head, int64_t seed) {
	int64_t acc = seed;
	for (; head != NULL; head = head->next)
		acc = head->fn(head, acc);
	return acc;
}

static int64_t walk_rec(const fl_node *n, int64_t acc) {
	if (n == NULL)
		return acc;
	acc = n->fn(n, acc);
	return walk_rec(n->next, acc);
}

int64_t fl_walk_rec(const fl_node *head, int64_t seed) {
	return walk_rec(head, seed);
}
