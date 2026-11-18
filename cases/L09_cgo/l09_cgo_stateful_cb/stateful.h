#ifndef L09_STATEFUL_H
#define L09_STATEFUL_H

#include <stdint.h>

/* 带状态回调：handle 是整数键（不是 Go 指针），Go 用它在自己的注册表里
 * 找到对应状态；accumulator 由 C 在本次调用内保存并逐步回传给 Go。 */
int64_t c_fold(const int32_t *vals, int32_t n, uintptr_t handle);
int64_t c_fold_two(const int32_t *a, int32_t na, uintptr_t ha,
		   const int32_t *b, int32_t nb, uintptr_t hb);

#endif
