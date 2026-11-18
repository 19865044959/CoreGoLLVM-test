#ifndef RA_H
#define RA_H

#include <stddef.h>
#include <stdint.h>

void *ra_realloc(void *p, int32_t n);
void ra_fill(void *p, int32_t from, int32_t to);
int64_t ra_sum(const void *p, int32_t n);
int ra_check_prefix(const void *p, int32_t n);
size_t ra_alignof_max(void);
size_t ra_mod(void *p, size_t align);
void ra_free(void *p);

#endif
