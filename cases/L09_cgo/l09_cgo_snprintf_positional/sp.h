#ifndef L09_SP_H
#define L09_SP_H

#include <stddef.h>

/* 固定参数包装：把 Go 侧强类型参数转发给 snprintf 的变参格式。 */
int sp_dyn_str(const char *fmt, int width, int prec, const char *s, char *buf, int cap);
int sp_dyn_int(const char *fmt, int width, int prec, long long v, char *buf, int cap);
int sp_pos_str_int(const char *fmt, const char *s, long long v, char *buf, int cap);
int sp_pos_int_str(const char *fmt, long long v, const char *s, char *buf, int cap);
int sp_pos_width(const char *fmt, long long v, int width, char *buf, int cap);
int sp_n_count(const char *s, int *n1, signed char *n2, char *buf, int cap);
int sp_size(const char *fmt, size_t v, char *buf, int cap);
int sp_intmax(const char *fmt, long long v, char *buf, int cap);
int sp_ptrdiff(const char *fmt, long v, char *buf, int cap);
int sp_hhd(const char *fmt, int v, char *buf, int cap);

#endif
