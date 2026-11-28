/* Z06: 每轮只新建一个 pthread，线程存活期仅本次回调；
 * 线程内通过 Go 导出的 goCb 调用回调，返回后 pthread_join 回收。
 * 线程不复用（不是常驻线程池），用于逼出 extra M/G 复用时陈旧的 gcstack。 */
#include <pthread.h>
#include <stdint.h>

extern int64_t goCb(int32_t iter, int64_t seed);

typedef int64_t (*z06_cb)(int32_t, int64_t);

typedef struct {
	z06_cb cb;
	int32_t iter;
	int64_t seed;
	int64_t result;
} z06_arg;

static void *z06_thread(void *p) {
	z06_arg *a = (z06_arg *)p;
	a->result = a->cb(a->iter, a->seed);
	return 0;
}

int64_t c_spawn_one(void *cb, int32_t iter, int64_t seed) {
	z06_arg a;
	pthread_t th;

	a.cb = (z06_cb)cb;
	a.iter = iter;
	a.seed = seed;
	a.result = 0;
	if (pthread_create(&th, 0, z06_thread, &a) != 0) {
		return -1;
	}
	if (pthread_join(th, 0) != 0) {
		return -2;
	}
	return a.result;
}

void *c_go_cb(void) { return (void *)goCb; }
