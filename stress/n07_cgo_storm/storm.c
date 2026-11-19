/* N07: C 侧多线程回调 Go（//export goCb），并提供一个阻塞函数供 Go 调用。 */
#include <pthread.h>
#include <stdint.h>
#include <time.h>

extern int64_t goCb(int32_t thread, int32_t iter, int32_t base);

typedef struct {
	int32_t tid;
	int32_t iters;
	int32_t base;
	int64_t sum;
} storm_arg;

static void *storm_thread(void *p) {
	storm_arg *a = (storm_arg *)p;
	int64_t s = 0;
	for (int32_t i = 0; i < a->iters; i++) {
		s += (int64_t)goCb(a->tid, i, a->base);
	}
	a->sum = s;
	return 0;
}

int64_t c_storm_run(int32_t nthreads, int32_t iters, int32_t base, int64_t *out_calls) {
	pthread_t th[64];
	storm_arg args[64];
	int32_t created = 0;

	if (nthreads <= 0 || nthreads > 64 || iters <= 0) {
		return -1;
	}
	for (int32_t t = 0; t < nthreads; t++) {
		args[t].tid = t;
		args[t].iters = iters;
		args[t].base = base;
		args[t].sum = 0;
		if (pthread_create(&th[t], 0, storm_thread, &args[t]) != 0) {
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

int32_t c_block_ms(int32_t ms) {
	struct timespec ts;
	ts.tv_sec = ms / 1000;
	ts.tv_nsec = (long)(ms % 1000) * 1000000L;
	return (int32_t)nanosleep(&ts, 0);
}
