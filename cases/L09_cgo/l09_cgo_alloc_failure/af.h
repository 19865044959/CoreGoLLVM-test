#ifndef L09_AF_H
#define L09_AF_H

#include <stddef.h>

/* C 堆分配失败边界：每类失败都在同一次调用内捕获 errno。 */
int af_posix(size_t align, size_t size, void **out);
int af_posix_huge(void **out);

void *af_malloc(size_t size, int *err);
void *af_malloc_max(int *err);
void *af_calloc(size_t nmemb, size_t size, int *err);
void *af_calloc_overflow(int *err);
void *af_realloc(void *p, size_t size, int *err);
void *af_realloc_max(void *p, int *err);

void af_free(void *p);
void af_fill(void *p, size_t n, unsigned char v);
int af_check(const void *p, size_t n, unsigned char v);
size_t af_align_max(void);

int af_einval(void);
int af_enomem(void);

#endif
