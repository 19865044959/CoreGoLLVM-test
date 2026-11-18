#ifndef VTABLE_H
#define VTABLE_H

#include <stddef.h>
#include <stdint.h>

typedef int64_t (*op_fn)(int64_t, int64_t);

typedef struct {
	const char *name;
	op_fn fn;
	int32_t arity;
	int32_t code;
} op_t;

typedef struct {
	op_t ops[4];
	int32_t n;
	int32_t bias;
	int64_t calls;
} vtable_t;

size_t vt_sizeof_op(void);
size_t vt_sizeof_vtable(void);
size_t vt_off_fn(void);
size_t vt_off_ops1(void);
size_t vt_off_bias(void);
size_t vt_off_calls(void);

void vt_init(vtable_t *vt);
const char *vt_name(vtable_t *vt, int idx);
int32_t vt_arity(vtable_t *vt, int idx);
int64_t vt_dispatch(vtable_t *vt, int idx, int64_t a, int64_t b);
int32_t vt_replace(vtable_t *vt, int idx, int32_t code);
int32_t vt_code(vtable_t *vt, int idx);
int vt_unique_fns(vtable_t *vt);
void vt_set_bias(vtable_t *vt, int32_t bias);
int64_t vt_calls(vtable_t *vt);
int vt_copy_name(vtable_t *vt, int idx, char *buf, size_t cap);

#endif
