#ifndef L09_QA_H
#define L09_QA_H

#include <stddef.h>
#include <stdint.h>

/* 4 个 int32 组成的定长记录：qsort 只搬移字节，Go/C 两侧布局必须一致。 */
typedef struct {
	int32_t key;
	int32_t tie;
	int32_t payload;
	int32_t pad;
} qa_rec_t;

/* qsort_r 的比较上下文：全部在 C 侧，Go 只读回统计值。 */
typedef struct {
	int32_t mode;
	int32_t calls;
	int32_t misaligned;
	int32_t pad;
	int64_t seed;
} qa_ctx_t;

size_t qa_rec_size(void);
size_t qa_rec_align(void);
size_t qa_off_key(void);
size_t qa_off_tie(void);
size_t qa_off_payload(void);
int qa_ctx_size(void);

/* 64 字节对齐分配 n 条记录；返回 posix_memalign 的原始错误码。 */
int qa_alloc(qa_rec_t **out, int n);
int qa_aligned(const void *p);
void qa_free(qa_rec_t *p);

/* 用 (i*13+5)%11 作为 key、i 作为唯一 tie 填充 [from,to)。 */
void qa_fill(qa_rec_t *p, int from, int to);

/* mode 0: key 升序、tie 升序；mode 1: key 降序、tie 升序。 */
void qa_sort(qa_rec_t *p, int n, qa_ctx_t *ctx, int mode);
int qa_ctx_calls(const qa_ctx_t *c);
int qa_ctx_misaligned(const qa_ctx_t *c);

/* 校验 payload == tie*7+1 且按 mode 有序；返回首个坏下标，-1 表示通过。 */
int qa_verify(const qa_rec_t *p, int n, int mode);

#endif
