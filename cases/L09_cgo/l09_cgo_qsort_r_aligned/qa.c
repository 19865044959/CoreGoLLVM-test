#define _GNU_SOURCE
#include "qa.h"

#include <stdlib.h>
#include <string.h>

size_t qa_rec_size(void) { return sizeof(qa_rec_t); }
size_t qa_rec_align(void) { return _Alignof(qa_rec_t); }
size_t qa_off_key(void) { return offsetof(qa_rec_t, key); }
size_t qa_off_tie(void) { return offsetof(qa_rec_t, tie); }
size_t qa_off_payload(void) { return offsetof(qa_rec_t, payload); }
int qa_ctx_size(void) { return (int)sizeof(qa_ctx_t); }

int qa_alloc(qa_rec_t **out, int n) {
	void *p = NULL;
	int rc = posix_memalign(&p, 64, (size_t)n * sizeof(qa_rec_t));
	if (rc != 0) {
		*out = NULL;
		return rc;
	}
	*out = (qa_rec_t *)p;
	return 0;
}

int qa_aligned(const void *p) { return ((uintptr_t)p & 63u) == 0; }

void qa_free(qa_rec_t *p) { free(p); }

void qa_fill(qa_rec_t *p, int from, int to) {
	for (int i = from; i < to; i++) {
		p[i].key = (int32_t)((i * 13 + 5) % 11);
		p[i].tie = (int32_t)i;
		p[i].payload = (int32_t)(i * 7 + 1);
		p[i].pad = 0;
	}
}

static int qa_cmp(const void *va, const void *vb, void *arg) {
	const qa_rec_t *a = (const qa_rec_t *)va;
	const qa_rec_t *b = (const qa_rec_t *)vb;
	qa_ctx_t *c = (qa_ctx_t *)arg;

	c->calls++;
	/* qsort_r 应始终把 4 字节对齐的元素地址交给比较器。 */
	if ((((uintptr_t)a | (uintptr_t)b) & 3u) != 0)
		c->misaligned++;
	c->seed = c->seed * 131 + a->tie - b->tie;

	if (a->key != b->key) {
		if (c->mode == 0)
			return a->key < b->key ? -1 : 1;
		return a->key < b->key ? 1 : -1;
	}
	if (a->tie != b->tie)
		return a->tie < b->tie ? -1 : 1;
	return 0;
}

void qa_sort(qa_rec_t *p, int n, qa_ctx_t *ctx, int mode) {
	ctx->mode = (int32_t)mode;
	ctx->calls = 0;
	ctx->misaligned = 0;
	ctx->seed = 0;
	if (n > 1)
		qsort_r(p, (size_t)n, sizeof(qa_rec_t), qa_cmp, ctx);
}

int qa_ctx_calls(const qa_ctx_t *c) { return (int)c->calls; }
int qa_ctx_misaligned(const qa_ctx_t *c) { return (int)c->misaligned; }

int qa_verify(const qa_rec_t *p, int n, int mode) {
	for (int i = 0; i < n; i++) {
		int32_t t = p[i].tie;
		if (t < 0 || t >= n)
			return i;
		if (p[i].key != (int32_t)((t * 13 + 5) % 11))
			return i;
		if (p[i].payload != (int32_t)(t * 7 + 1))
			return i;
		if (p[i].pad != 0)
			return i;
		if (i == 0)
			continue;
		if (mode == 0) {
			if (p[i - 1].key > p[i].key)
				return i;
			if (p[i - 1].key == p[i].key && p[i - 1].tie > p[i].tie)
				return i;
		} else {
			if (p[i - 1].key < p[i].key)
				return i;
			if (p[i - 1].key == p[i].key && p[i - 1].tie > p[i].tie)
				return i;
		}
	}
	return -1;
}
