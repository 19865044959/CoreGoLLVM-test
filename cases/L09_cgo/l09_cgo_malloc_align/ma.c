#define _GNU_SOURCE
#include "ma.h"

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>

/* posix_memalign 成功返回 0 并写指针；失败返回错误码且不写指针。 */
void *ma_alloc(size_t align, size_t size, int *rc) {
	void *p = NULL;
	int r = posix_memalign(&p, align, size);
	if (rc) *rc = r;
	if (r != 0) return NULL;
	return p;
}

void *ma_malloc(size_t size) { return malloc(size); }

void ma_fill(uint8_t *p, size_t n, uint32_t seed) {
	for (size_t i = 0; i < n; i++) {
		p[i] = (uint8_t)(seed ^ (uint32_t)(i * 131u + 17u));
	}
}

uint32_t ma_hash(const uint8_t *p, size_t n) {
	uint32_t h = 2166136261u;
	for (size_t i = 0; i < n; i++) {
		h ^= p[i];
		h *= 16777619u;
	}
	return h;
}

size_t ma_mod(void *p, size_t align) {
	return (size_t)((uintptr_t)p % align);
}

size_t ma_alignof_max(void) { return _Alignof(max_align_t); }

int ma_einval(void) { return EINVAL; }

void ma_free(void *p) { free(p); }
