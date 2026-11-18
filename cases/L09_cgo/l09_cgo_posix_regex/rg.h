#ifndef RG_H
#define RG_H

#include <stdint.h>

typedef struct {
	int32_t s;
	int32_t e;
} mr_t;

int rg_exec(const char *pat, const char *text, mr_t *out, int max, int *outn);
int rg_max_ranges(void);

#endif
