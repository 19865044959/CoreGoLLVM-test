#include "sv.h"

#include <stdint.h>
#include <stdio.h>

/* cgo 不能直接调用 C 变参函数，实际绑定库都用固定参数包装层。
 * 这里让包装层把参数转给 snprintf，覆盖宽度/精度/标志/截断语义。 */

int sv_int(char *buf, size_t cap, const char *fmt, int64_t v) {
	if (!fmt) return -1;
	return snprintf(buf, cap, fmt, (long long)v);
}

int sv_uint(char *buf, size_t cap, const char *fmt, uint64_t v) {
	if (!fmt) return -1;
	return snprintf(buf, cap, fmt, (unsigned long long)v);
}

int sv_double(char *buf, size_t cap, const char *fmt, double v) {
	if (!fmt) return -1;
	return snprintf(buf, cap, fmt, v);
}

int sv_str(char *buf, size_t cap, const char *fmt, const char *s) {
	if (!fmt) return -1;
	return snprintf(buf, cap, fmt, s ? s : "(null)");
}

int sv_mixed(char *buf, size_t cap, const char *fmt, int32_t a, uint32_t b, double c, const char *s) {
	if (!fmt) return -1;
	return snprintf(buf, cap, fmt, (int)a, (unsigned int)b, c, s ? s : "(null)");
}
