#include "stateful.h"

/* 由 main.go 的 //export goStateFold 生成；签名与 Go 侧严格一致。 */
extern int64_t goStateFold(uintptr_t handle, int64_t acc, int32_t v);

int64_t c_fold(const int32_t *vals, int32_t n, uintptr_t handle) {
	int64_t acc = 0;
	for (int32_t i = 0; i < n; i++) {
		acc = goStateFold(handle, acc, vals[i]);
	}
	return acc;
}

int64_t c_fold_two(const int32_t *a, int32_t na, uintptr_t ha,
		   const int32_t *b, int32_t nb, uintptr_t hb) {
	int64_t acc = 0;
	for (int32_t i = 0; i < na; i++) {
		acc = goStateFold(ha, acc, a[i]);
	}
	for (int32_t i = 0; i < nb; i++) {
		acc = goStateFold(hb, acc, b[i]);
	}
	return acc;
}
