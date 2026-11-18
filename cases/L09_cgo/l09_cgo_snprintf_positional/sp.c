#include "sp.h"

#include <stdint.h>
#include <stdio.h>

int sp_dyn_str(const char *fmt, int width, int prec, const char *s, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, width, prec, s);
}

int sp_dyn_int(const char *fmt, int width, int prec, long long v, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, width, prec, v);
}

int sp_pos_str_int(const char *fmt, const char *s, long long v, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, s, v);
}

int sp_pos_int_str(const char *fmt, long long v, const char *s, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, v, s);
}

int sp_pos_width(const char *fmt, long long v, int width, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, v, width);
}

int sp_n_count(const char *s, int *n1, signed char *n2, char *buf, int cap) {
	/* 只读字面量格式：glibc FORTIFY 禁止在可写格式串中使用 %n。 */
	static const char fmt[] = "%s%n-%hhn";
	return snprintf(buf, (size_t)cap, fmt, s, n1, n2);
}

int sp_size(const char *fmt, size_t v, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, v);
}

int sp_intmax(const char *fmt, long long v, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, (intmax_t)v);
}

int sp_ptrdiff(const char *fmt, long v, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, (ptrdiff_t)v);
}

int sp_hhd(const char *fmt, int v, char *buf, int cap) {
	return snprintf(buf, (size_t)cap, fmt, (signed char)v);
}
