#ifndef L09_CSORT_H
#define L09_CSORT_H

#include <stdint.h>

typedef struct person {
	int32_t id;
	int32_t age;
	int32_t score;
	char name[12];
} person_t;

person_t *c_people_new(int32_t n);
void c_people_free(person_t *p);
void c_people_fill(person_t *p, int32_t n);
void c_sort_people(person_t *p, int32_t n);
int32_t c_people_is_sorted(const person_t *p, int32_t n);
int64_t c_people_checksum(const person_t *p, int32_t n);
void c_sort_i32_asc(int32_t *a, int32_t n);

#endif
