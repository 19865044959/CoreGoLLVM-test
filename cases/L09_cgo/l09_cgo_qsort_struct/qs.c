#include "qs.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* goPersonCmp / goScoreCmp 来自 main.go 的 //export（cgo 生成 C 符号）。 */
extern int goPersonCmp(void *a, void *b);
extern int goScoreCmp(void *a, void *b);

typedef int (*cmp_t)(const void *, const void *);

void qs_fill(person_t *a, int32_t n) {
	uint32_t s = 0x12345678u;
	for (int32_t i = 0; i < n; i++) {
		/* i*常数 mod 100003 对 i<100003 是双射，保证 score 唯一，
		 * 排序结果因此完全确定（qsort 不稳定也不影响断言）。 */
		s = s * 1664525u + 1013904223u;
		a[i].score = (int32_t)(((uint32_t)i * 2654435761u) % 100003u);
		a[i].age = 18 + (int32_t)(s % 50u);
		a[i].id = i;
		char tmp[16];
		int m = snprintf(tmp, sizeof(tmp), "rec%03d", (int)i);
		if (m < 0) m = 0;
		if (m > (int)sizeof(a[i].name) - 1) m = (int)sizeof(a[i].name) - 1;
		memcpy(a[i].name, tmp, (size_t)m);
		a[i].name[m] = '\0';
	}
}

void qs_sort(person_t *a, int32_t n) {
	if (n > 1) {
		qsort(a, (size_t)n, sizeof(person_t), (cmp_t)goPersonCmp);
	}
}

int32_t qs_bsearch_score(person_t *a, int32_t n, int32_t score) {
	person_t key;
	if (n <= 0) return -1;
	memset(&key, 0, sizeof(key));
	key.score = score;
	person_t *r = (person_t *)bsearch(&key, a, (size_t)n, sizeof(person_t), (cmp_t)goScoreCmp);
	if (!r) return -1;
	return (int32_t)(r - a);
}

static int local_cmp(const person_t *x, const person_t *y) {
	if (x->score != y->score) return x->score > y->score ? -1 : 1;
	if (x->age != y->age) return x->age < y->age ? -1 : 1;
	int c = strcmp(x->name, y->name);
	if (c != 0) return c;
	if (x->id != y->id) return x->id < y->id ? -1 : 1;
	return 0;
}

int32_t qs_check_sorted(person_t *a, int32_t n) {
	for (int32_t i = 1; i < n; i++) {
		if (local_cmp(&a[i - 1], &a[i]) > 0) return i;
	}
	return 0;
}

int64_t qs_sum_scores(person_t *a, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) s += a[i].score;
	return s;
}

size_t qs_sizeof_person(void) { return sizeof(person_t); }
size_t qs_off_name(void) { return offsetof(person_t, name); }
size_t qs_off_age(void) { return offsetof(person_t, age); }
