#define _GNU_SOURCE
#include "af.h"

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>

int af_posix(size_t align, size_t size, void **out) {
	*out = NULL;
	return posix_memalign(out, align, size);
}

int af_posix_huge(void **out) {
	*out = NULL;
	/* SIZE_MAX 级请求必然超出地址空间，glibc 返回 ENOMEM。 */
	return posix_memalign(out, 64, SIZE_MAX - 4095);
}

void *af_malloc(size_t size, int *err) {
	errno = 0;
	void *p = malloc(size);
	*err = errno;
	return p;
}

void *af_malloc_max(int *err) { return af_malloc(SIZE_MAX, err); }

void *af_calloc(size_t nmemb, size_t size, int *err) {
	errno = 0;
	void *p = calloc(nmemb, size);
	*err = errno;
	return p;
}

void *af_calloc_overflow(int *err) {
	size_t nmemb = SIZE_MAX / 4 + 1;
	errno = 0;
	void *p = calloc(nmemb, (size_t)8);
	*err = errno;
	return p;
}

void *af_realloc(void *p, size_t size, int *err) {
	errno = 0;
	void *q = realloc(p, size);
	*err = errno;
	return q;
}

void *af_realloc_max(void *p, int *err) { return af_realloc(p, SIZE_MAX, err); }

void af_free(void *p) { free(p); }

void af_fill(void *vp, size_t n, unsigned char v) {
	unsigned char *p = (unsigned char *)vp;
	for (size_t i = 0; i < n; i++)
		p[i] = v;
}

int af_check(const void *vp, size_t n, unsigned char v) {
	const unsigned char *p = (const unsigned char *)vp;
	for (size_t i = 0; i < n; i++)
		if (p[i] != v)
			return (int)i;
	return -1;
}

size_t af_align_max(void) { return _Alignof(max_align_t); }
int af_einval(void) { return EINVAL; }
int af_enomem(void) { return ENOMEM; }
