#include <stdint.h>

// goCallback 由 main.go 的 //export 生成（cgo 会生成 C 符号）。
extern int32_t goCallback(int32_t x);

typedef int32_t (*cb_t)(int32_t);

static int32_t invoke(cb_t fn, int32_t x) {
	return fn(x);
}

int32_t c_invoke_go(int32_t x) {
	return invoke(goCallback, x);
}

int32_t c_invoke_go_n(int32_t x, int32_t n) {
	int32_t s = 0;
	for (int32_t i = 0; i < n; i++) {
		s += invoke(goCallback, x + i);
	}
	return s;
}
