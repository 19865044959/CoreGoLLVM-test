#ifndef QS_H
#define QS_H

#include <stddef.h>
#include <stdint.h>

typedef struct {
	int32_t score;
	int32_t age;
	int32_t id;
	char name[12];
} person_t;

void qs_fill(person_t *a, int32_t n);
void qs_sort(person_t *a, int32_t n);
int32_t qs_bsearch_score(person_t *a, int32_t n, int32_t score);
int32_t qs_check_sorted(person_t *a, int32_t n);
int64_t qs_sum_scores(person_t *a, int32_t n);
size_t qs_sizeof_person(void);
size_t qs_off_name(void);
size_t qs_off_age(void);

#endif
