#include <stdint.h>
#include <stdlib.h>

// 布局需与 Go 侧 udLayout（三个 int64）一致。
struct ud_s {
	int64_t calls;
	int64_t sum;
	int64_t mul;
};

// goUDApply 由 main.go 的 //export 生成。
extern int64_t goUDApply(void *ud, int64_t x);

int64_t c_ud_size(void) { return (int64_t)sizeof(struct ud_s); }

struct ud_s *c_ud_new(int64_t mul) {
	struct ud_s *p = (struct ud_s *)calloc(1, sizeof(*p));
	if (p) p->mul = mul;
	return p;
}

void c_ud_free(struct ud_s *p) { free(p); }

int64_t c_ud_calls(const struct ud_s *p) { return p->calls; }
int64_t c_ud_sum(const struct ud_s *p) { return p->sum; }
int64_t c_ud_mul(const struct ud_s *p) { return p->mul; }

// C 只在本调用期内把 userdata 指针传给回调，返回后不保留。
int64_t c_apply_ud(struct ud_s *ud, int64_t x) {
	return goUDApply(ud, x);
}

int64_t c_apply_ud_n(struct ud_s *ud, int64_t x, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) {
		s += goUDApply(ud, x + i);
	}
	return s;
}
