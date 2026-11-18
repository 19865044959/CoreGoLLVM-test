#include <stdint.h>
#include <stdlib.h>

// goIntCmp 由 main.go 的 //export 生成（cgo 生成 C 符号），
// 签名对应 Go 侧 func goIntCmp(a, b unsafe.Pointer) C.int。
extern int goIntCmp(void *a, void *b);

typedef int (*cmp_t)(const void *, const void *);

// qsort 的比较器类型带 const，函数指针需显式转换；
// C 只在本次调用期内持有 a 与 goIntCmp，返回后不保留。
void c_sort_ints(int32_t *a, int32_t n) {
	if (n > 1) {
		qsort(a, (size_t)n, sizeof(int32_t), (cmp_t)goIntCmp);
	}
}
