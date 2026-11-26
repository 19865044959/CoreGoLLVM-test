/* X08: C 侧信号栈/换栈回调与外部信号洪水。
 *
 * - sig_thread：C pthread 先 sigaltstack 自建信号栈，再回调 //export goSigCb；
 *   回调内由 Go 制造 SIGSEGV（nil deref / SetPanicOnFault）并在返回 C 前 recover。
 * - flood_thread：独立 C pthread 持续向进程发 SIGURG/SIGPROF（进程级 kill）。
 *   该线程自阻塞这两个信号，避免自己收信号打断 usleep；其他线程照常接收，
 *   Go runtime 的信号处理器在 gsignal/用户 altstack 上运行。
 * - c_stack_switch_run：glibc ucontext(makecontext/swapcontext) 换到自建 C 栈后
 *   回调 Go（对应 Go testprogcgo StackSwitchCallback），再换回原栈。
 */
#define _GNU_SOURCE
#define _XOPEN_SOURCE 700

#include <pthread.h>
#include <signal.h>
#include <stdatomic.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <time.h>
#include <unistd.h>

#if defined(__linux__) && defined(__GLIBC__)
#include <ucontext.h>
#define X08_HAVE_UCONTEXT 1
#endif

extern int64_t goSigCb(int32_t thread, int32_t iter, int32_t base, int32_t sub);

#define X08_SIGSTKSZ (64u << 10)
#define X08_SSSZ (64u << 10)
#define X08_MAXT 8

typedef struct {
	int32_t tid;
	int32_t iters;
	int32_t base;
	int32_t sub;
	int32_t altstack;
	int32_t err;
	int64_t sum;
} sig_arg;

static int install_altstack(void **base_out, stack_t *old_out) {
	void *base = mmap(0, X08_SIGSTKSZ, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	if (base == MAP_FAILED) {
		return -1;
	}
	stack_t st;
	memset(&st, 0, sizeof(st));
	st.ss_sp = base;
	st.ss_size = X08_SIGSTKSZ;
	if (sigaltstack(&st, old_out) != 0) {
		munmap(base, X08_SIGSTKSZ);
		return -1;
	}
	*base_out = base;
	return 0;
}

static void restore_altstack(void *base, stack_t *old) {
	if (base == 0) {
		return;
	}
	sigaltstack(old, 0);
	mprotect(base, X08_SIGSTKSZ, PROT_NONE); /* 恢复后再被写入即 SIGSEGV */
	munmap(base, X08_SIGSTKSZ);
}

static void *sig_thread(void *p) {
	sig_arg *a = (sig_arg *)p;
	void *alt = 0;
	stack_t old;
	int64_t s = 0;

	if (a->altstack) {
		if (install_altstack(&alt, &old) != 0) {
			a->err = 1;
			return 0;
		}
	}
	for (int32_t i = 0; i < a->iters; i++) {
		s += (int64_t)goSigCb(a->tid, i, a->base, a->sub);
	}
	a->sum = s;
	if (a->altstack) {
		restore_altstack(alt, &old);
	}
	return 0;
}

/* altstack=1 时偶数线程装用户信号栈、奇数线程不装，覆盖 issue #22930 两条路径。 */
int64_t c_sig_storm(int32_t nthreads, int32_t iters, int32_t base, int32_t sub, int32_t altstack, int64_t *out_calls) {
	pthread_t th[X08_MAXT];
	sig_arg args[X08_MAXT];
	int32_t created = 0;

	if (nthreads <= 0 || nthreads > X08_MAXT || iters <= 0) {
		return -2;
	}
	for (int32_t t = 0; t < nthreads; t++) {
		args[t].tid = t;
		args[t].iters = iters;
		args[t].base = base;
		args[t].sub = sub;
		args[t].altstack = altstack && (t % 2 == 0);
		args[t].err = 0;
		args[t].sum = 0;
		if (pthread_create(&th[t], 0, sig_thread, &args[t]) != 0) {
			for (int32_t j = 0; j < created; j++) {
				pthread_join(th[j], 0);
			}
			return -2;
		}
		created++;
	}

	int64_t total = 0;
	int32_t err = 0;
	for (int32_t t = 0; t < nthreads; t++) {
		pthread_join(th[t], 0);
		total += args[t].sum;
		err |= args[t].err;
	}
	*out_calls = (int64_t)nthreads * (int64_t)iters;
	return err ? -3 : total;
}

/* ---- 外部线程信号洪水：SIGURG（异步抢占）+ SIGPROF（profiling 信号） ---- */

static pthread_t flood_tid;
static atomic_int flood_running;
static atomic_int flood_stop;
static atomic_int flood_hold; /* 暂停计数：多个 worker 可同时暂停换栈 */
static atomic_llong flood_sent;

static void *flood_thread(void *p) {
	(void)p;
	sigset_t set;
	sigemptyset(&set);
	sigaddset(&set, SIGURG);
	sigaddset(&set, SIGPROF);
	pthread_sigmask(SIG_BLOCK, &set, 0); /* 洪水线程自己收不到，避免打断 nanosleep */
	pid_t pid = getpid();
	struct timespec ts;
	ts.tv_sec = 0;
	ts.tv_nsec = 250000; /* 250us */
	while (!atomic_load(&flood_stop)) {
		if (atomic_load(&flood_hold) > 0) {
			ts.tv_nsec = 100000;
			nanosleep(&ts, 0);
			continue;
		}
		kill(pid, SIGURG);
		atomic_fetch_add(&flood_sent, 1);
		ts.tv_nsec = 250000;
		nanosleep(&ts, 0);
		if (atomic_load(&flood_hold) > 0) {
			continue;
		}
		kill(pid, SIGPROF);
		atomic_fetch_add(&flood_sent, 1);
		ts.tv_nsec = 500000;
		nanosleep(&ts, 0);
	}
	return 0;
}

int32_t c_flood_start(void) {
	int expected = 0;
	if (!atomic_compare_exchange_strong(&flood_running, &expected, 1)) {
		return 0; /* 已在运行 */
	}
	atomic_store(&flood_stop, 0);
	if (pthread_create(&flood_tid, 0, flood_thread, 0) != 0) {
		atomic_store(&flood_running, 0);
		return -1;
	}
	return 0;
}

/* 换栈子模式期间暂停洪水：stock Go 1.24.1 上“SIGURG+SIGPROF 双洪水 +
 * ucontext 换栈回调”组合会 hang（两种信号各自单独跑稳定），见 scenario.md。 */
void c_flood_pause(void) {
	atomic_fetch_add(&flood_hold, 1);
}

void c_flood_resume(void) {
	atomic_fetch_sub(&flood_hold, 1);
}

int32_t c_flood_stop(int64_t *sent) {
	if (!atomic_load(&flood_running)) {
		if (sent) {
			*sent = atomic_load(&flood_sent);
		}
		return 0;
	}
	atomic_store(&flood_stop, 1);
	pthread_join(flood_tid, 0);
	atomic_store(&flood_running, 0);
	if (sent) {
		*sent = atomic_load(&flood_sent);
	}
	return 0;
}

/* ---- C 栈切换后回调 Go（ucontext；musl 无 getcontext 时返回 -4） ---- */

#ifdef X08_HAVE_UCONTEXT
typedef struct {
	ucontext_t save;
	ucontext_t uc;
	char *stk[2];
	int32_t tid;
	int32_t iters;
	int32_t base;
	int32_t iter;
	int32_t altstack;
	int32_t err;
	int cur;
	int64_t sum;
} ss_arg;

/* makecontext 只保证传 int 参数；用 TLS 传当前线程的 ss_arg 指针（同线程内换栈不影响 TLS）。 */
static __thread ss_arg *ss_cur;

static void ss_entry(void) {
	ss_arg *a = ss_cur;
	a->sum += (int64_t)goSigCb(a->tid, a->iter, a->base, 3);
}

static void *ss_thread(void *p) {
	ss_arg *a = (ss_arg *)p;
	void *alt = 0;
	stack_t old;

	ss_cur = a;

	if (a->altstack && install_altstack(&alt, &old) != 0) {
		a->err = 1;
		return 0;
	}
	/* 先在普通线程栈上回调一次，让 runtime 记录该 C 线程的栈边界（issue #62440）。 */
	(void)goSigCb(a->tid, -1, a->base, 3);
	for (int32_t i = 0; i < a->iters; i++) {
		a->iter = i;
		a->cur ^= 1;
		if (getcontext(&a->uc) != 0) {
			a->err = 2;
			break;
		}
		a->uc.uc_stack.ss_sp = a->stk[a->cur];
		a->uc.uc_stack.ss_size = X08_SSSZ;
		a->uc.uc_link = &a->save;
		makecontext(&a->uc, (void (*)(void))ss_entry, 0);
		if (swapcontext(&a->save, &a->uc) != 0) {
			a->err = 3;
			break;
		}
	}
	if (a->altstack) {
		restore_altstack(alt, &old);
	}
	return 0;
}
#endif

int64_t c_stack_switch_run(int32_t nthreads, int32_t iters, int32_t base, int32_t altstack, int64_t *out_calls) {
#ifdef X08_HAVE_UCONTEXT
	pthread_t th[X08_MAXT];
	ss_arg args[X08_MAXT];
	int32_t created = 0;

	if (nthreads <= 0 || nthreads > X08_MAXT || iters <= 0) {
		return -2;
	}
	for (int32_t t = 0; t < nthreads; t++) {
		ss_arg *a = &args[t];
		memset(a, 0, sizeof(*a));
		a->tid = t;
		a->iters = iters;
		a->base = base;
		a->altstack = altstack;
		a->stk[0] = malloc(X08_SSSZ);
		a->stk[1] = malloc(X08_SSSZ);
		if (a->stk[0] == 0 || a->stk[1] == 0) {
			free(a->stk[0]);
			free(a->stk[1]);
			for (int32_t j = 0; j < created; j++) {
				pthread_join(th[j], 0);
			}
			return -2;
		}
		if (pthread_create(&th[t], 0, ss_thread, a) != 0) {
			free(a->stk[0]);
			free(a->stk[1]);
			for (int32_t j = 0; j < created; j++) {
				pthread_join(th[j], 0);
			}
			return -2;
		}
		created++;
	}

	int64_t total = 0;
	int32_t err = 0;
	for (int32_t t = 0; t < nthreads; t++) {
		pthread_join(th[t], 0);
		total += args[t].sum;
		err |= args[t].err;
		free(args[t].stk[0]);
		free(args[t].stk[1]);
	}
	*out_calls = (int64_t)nthreads * (int64_t)iters;
	return err ? -3 : total;
#else
	(void)nthreads;
	(void)iters;
	(void)base;
	(void)altstack;
	*out_calls = 0;
	return -4; /* 平台无 ucontext，Go 侧跳过子模式 3 */
#endif
}
