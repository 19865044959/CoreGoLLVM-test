/* X07: C 侧 pthread 反复回调 Go（//export goCbPanic）。
 *
 * 每次回调内部由 Go 完成深递归/GC/panic/recover；C 线程在回调返回后继续下一次
 * 回调，从而覆盖“panic 在 C→Go 回调帧内发生并被 recover，返回 C 后再次进入 Go”。
 */
#include <pthread.h>
#include <stdint.h>

extern int64_t goCbPanic(int32_t thread, int32_t iter, int32_t base, int32_t mode);

typedef struct {
	int32_t tid;
	int32_t iters;
	int32_t base;
	int32_t mode;
	int64_t sum;
} panic_arg;

static void *panic_thread(void *p) {
	panic_arg *a = (panic_arg *)p;
	int64_t s = 0;
	for (int32_t i = 0; i < a->iters; i++) {
		s += (int64_t)goCbPanic(a->tid, i, a->base, a->mode);
	}
	a->sum = s;
	return 0;
}

int64_t c_panic_storm(int32_t nthreads, int32_t iters, int32_t base, int32_t mode, int64_t *out_calls) {
	pthread_t th[8];
	panic_arg args[8];
	int32_t created = 0;

	if (nthreads <= 0 || nthreads > 8 || iters <= 0) {
		return -1;
	}
	for (int32_t t = 0; t < nthreads; t++) {
		args[t].tid = t;
		args[t].iters = iters;
		args[t].base = base;
		args[t].mode = mode;
		args[t].sum = 0;
		if (pthread_create(&th[t], 0, panic_thread, &args[t]) != 0) {
			for (int32_t j = 0; j < created; j++) {
				pthread_join(th[j], 0);
			}
			return -1;
		}
		created++;
	}

	int64_t total = 0;
	for (int32_t t = 0; t < nthreads; t++) {
		pthread_join(th[t], 0);
		total += args[t].sum;
	}
	*out_calls = (int64_t)nthreads * (int64_t)iters;
	return total;
}
