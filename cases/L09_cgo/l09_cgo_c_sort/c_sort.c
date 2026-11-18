#include "c_sort.h"

#include <stdlib.h>

person_t *c_people_new(int32_t n) {
	if (n <= 0) {
		return NULL;
	}
	return (person_t *)calloc((size_t)n, sizeof(person_t));
}

void c_people_free(person_t *p) {
	free(p);
}

void c_people_fill(person_t *p, int32_t n) {
	for (int32_t i = 0; i < n; i++) {
		p[i].id = i;
		p[i].age = 20 + (i * 13) % 40;
		p[i].score = (i * 17) % 7;
		p[i].name[0] = 'p';
		p[i].name[1] = (char)('0' + (i / 100) % 10);
		p[i].name[2] = (char)('0' + (i / 10) % 10);
		p[i].name[3] = (char)('0' + i % 10);
		p[i].name[4] = 0;
	}
}

/* 稳定插入排序：score 降序、age 升序；score/age 相同保持原有 id 顺序。
 * 只在 key 严格排在被比较元素之前时移动，因此相同键不会交换。 */
static int person_before(const person_t *a, const person_t *b) {
	if (a->score != b->score) {
		return a->score > b->score;
	}
	if (a->age != b->age) {
		return a->age < b->age;
	}
	return 0;
}

void c_sort_people(person_t *p, int32_t n) {
	for (int32_t i = 1; i < n; i++) {
		person_t key = p[i];
		int32_t j = i - 1;
		while (j >= 0 && person_before(&key, &p[j])) {
			p[j + 1] = p[j];
			j--;
		}
		p[j + 1] = key;
	}
}

int32_t c_people_is_sorted(const person_t *p, int32_t n) {
	for (int32_t i = 1; i < n; i++) {
		if (person_before(&p[i], &p[i - 1])) {
			return 0;
		}
	}
	return 1;
}

int64_t c_people_checksum(const person_t *p, int32_t n) {
	int64_t s = 0;
	for (int32_t i = 0; i < n; i++) {
		s += (int64_t)p[i].id + (int64_t)p[i].age * 3 + (int64_t)p[i].score * 5;
	}
	return s;
}

static void merge_i32(int32_t *a, int32_t *tmp, int32_t l, int32_t m, int32_t r) {
	int32_t i = l, j = m, k = l;
	while (i < m && j < r) {
		if (a[i] <= a[j]) {
			tmp[k++] = a[i++];
		} else {
			tmp[k++] = a[j++];
		}
	}
	while (i < m) {
		tmp[k++] = a[i++];
	}
	while (j < r) {
		tmp[k++] = a[j++];
	}
	for (i = l; i < r; i++) {
		a[i] = tmp[i];
	}
}

static void merge_sort_i32(int32_t *a, int32_t *tmp, int32_t l, int32_t r) {
	if (r - l < 2) {
		return;
	}
	int32_t m = l + (r - l) / 2;
	merge_sort_i32(a, tmp, l, m);
	merge_sort_i32(a, tmp, m, r);
	merge_i32(a, tmp, l, m, r);
}

void c_sort_i32_asc(int32_t *a, int32_t n) {
	if (n < 2) {
		return;
	}
	int32_t *tmp = (int32_t *)malloc((size_t)n * sizeof(int32_t));
	if (tmp == NULL) {
		return;
	}
	merge_sort_i32(a, tmp, 0, n);
	free(tmp);
}
