#include "vtable.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

/* 回绕语义用无符号运算定义，避免 C 有符号溢出 UB。 */
static int64_t op_add(int64_t a, int64_t b) {
	return (int64_t)((uint64_t)a + (uint64_t)b);
}

static int64_t op_mul(int64_t a, int64_t b) {
	return (int64_t)((uint64_t)a * (uint64_t)b);
}

static int64_t op_max(int64_t a, int64_t b) {
	return a > b ? a : b;
}

static int64_t op_min(int64_t a, int64_t b) {
	return a < b ? a : b;
}

static int32_t fn_code(op_fn f) {
	if (f == op_add) return 1;
	if (f == op_mul) return 2;
	if (f == op_max) return 3;
	if (f == op_min) return 4;
	return 0;
}

size_t vt_sizeof_op(void) { return sizeof(op_t); }
size_t vt_sizeof_vtable(void) { return sizeof(vtable_t); }
size_t vt_off_fn(void) { return offsetof(op_t, fn); }
size_t vt_off_ops1(void) { return offsetof(vtable_t, ops[1]); }
size_t vt_off_bias(void) { return offsetof(vtable_t, bias); }
size_t vt_off_calls(void) { return offsetof(vtable_t, calls); }

void vt_init(vtable_t *vt) {
	if (!vt) return;
	vt->ops[0] = (op_t){"add", op_add, 2, 1};
	vt->ops[1] = (op_t){"mul", op_mul, 2, 2};
	vt->ops[2] = (op_t){"max", op_max, 2, 3};
	vt->ops[3] = (op_t){"min", op_min, 2, 4};
	vt->n = 4;
	vt->bias = 0;
	vt->calls = 0;
}

const char *vt_name(vtable_t *vt, int idx) {
	if (!vt || idx < 0 || idx >= vt->n) return NULL;
	return vt->ops[idx].name;
}

int32_t vt_arity(vtable_t *vt, int idx) {
	if (!vt || idx < 0 || idx >= vt->n) return -1;
	return vt->ops[idx].arity;
}

int64_t vt_dispatch(vtable_t *vt, int idx, int64_t a, int64_t b) {
	if (!vt || idx < 0 || idx >= vt->n || !vt->ops[idx].fn) return INT64_MIN;
	vt->calls++;
	return vt->ops[idx].fn(a, b) + (int64_t)vt->bias;
}

int32_t vt_replace(vtable_t *vt, int idx, int32_t code) {
	op_fn f;
	if (!vt || idx < 0 || idx >= vt->n) return -1;
	switch (code) {
	case 1: f = op_add; break;
	case 2: f = op_mul; break;
	case 3: f = op_max; break;
	case 4: f = op_min; break;
	default: return -1;
	}
	int32_t old = fn_code(vt->ops[idx].fn);
	vt->ops[idx].fn = f;
	vt->ops[idx].code = code;
	return old;
}

int32_t vt_code(vtable_t *vt, int idx) {
	if (!vt || idx < 0 || idx >= vt->n) return -1;
	return fn_code(vt->ops[idx].fn);
}

int vt_unique_fns(vtable_t *vt) {
	int n = 0;
	if (!vt) return 0;
	for (int i = 0; i < vt->n; i++) {
		int dup = 0;
		for (int j = 0; j < i; j++) {
			if (vt->ops[j].fn == vt->ops[i].fn) {
				dup = 1;
				break;
			}
		}
		if (!dup) n++;
	}
	return n;
}

void vt_set_bias(vtable_t *vt, int32_t bias) {
	if (vt) vt->bias = bias;
}

int64_t vt_calls(vtable_t *vt) {
	return vt ? vt->calls : -1;
}

int vt_copy_name(vtable_t *vt, int idx, char *buf, size_t cap) {
	const char *s;
	size_t n;
	if (!vt || idx < 0 || idx >= vt->n) {
		if (buf && cap) buf[0] = '\0';
		return -1;
	}
	s = vt->ops[idx].name;
	n = strlen(s);
	if (!buf || cap < n + 1) {
		if (buf && cap) buf[0] = '\0';
		return -1;
	}
	memcpy(buf, s, n + 1);
	return (int)n;
}
