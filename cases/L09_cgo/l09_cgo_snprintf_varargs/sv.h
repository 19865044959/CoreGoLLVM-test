#ifndef SV_H
#define SV_H

#include <stddef.h>
#include <stdint.h>

int sv_int(char *buf, size_t cap, const char *fmt, int64_t v);
int sv_uint(char *buf, size_t cap, const char *fmt, uint64_t v);
int sv_double(char *buf, size_t cap, const char *fmt, double v);
int sv_str(char *buf, size_t cap, const char *fmt, const char *s);
int sv_mixed(char *buf, size_t cap, const char *fmt, int32_t a, uint32_t b, double c, const char *s);

#endif
