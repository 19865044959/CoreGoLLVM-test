#define _GNU_SOURCE
#include "ra.h"

#include <stdint.h>
#include <stdlib.h>

/* realloc(NULL, n) 等价 malloc；缩小保留前缀；新扩展区内容未初始化，
 * 由调用方先写后读。所有内存归 C 堆所有。 */
void *ra_realloc(void *p, int32_t n) {
	if (n < 0) return NULL;
	return realloc(p, (size_t)n * sizeof(int32_t));
}

void ra_fill(void *p, int32_t from, int32_t to) {
	int32_t *a = (int32_t *)p;
	for (int32_t i = from; i < to; i++) {
		a[i] = i * 3 + 1;
	}
}

int64_t ra_sum(const void *p, int32_t n) {
	const int32_t *a = (const int32_t *)p;
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) s += a[i];
	return s;
}

int ra_check_prefix(const void *p, int32_t n) {
	const int32_t *a = (const int32_t *)p;
	for (int32_t i = 0; i < n; i++) {
		if (a[i] != i * 3 + 1) return i;
	}
	return -1;
}

size_t ra_alignof_max(void) { return _Alignof(max_align_t); }

size_t ra_mod(void *p, size_t align) {
	return (size_t)((uintptr_t)p % align);
}

void ra_free(void *p) { free(p); }
