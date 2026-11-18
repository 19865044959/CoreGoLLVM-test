#ifndef MA_H
#define MA_H

#include <stddef.h>
#include <stdint.h>

void *ma_alloc(size_t align, size_t size, int *rc);
void *ma_malloc(size_t size);
void ma_fill(uint8_t *p, size_t n, uint32_t seed);
uint32_t ma_hash(const uint8_t *p, size_t n);
size_t ma_mod(void *p, size_t align);
size_t ma_alignof_max(void);
int ma_einval(void);
void ma_free(void *p);

#endif
