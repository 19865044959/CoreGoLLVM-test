#include "rs.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

size_t rs_size(void) { return sizeof(rs_rec_t); }
size_t rs_align(void) { return _Alignof(rs_rec_t); }
size_t rs_off_name(void) { return offsetof(rs_rec_t, name); }
size_t rs_off_len(void) { return offsetof(rs_rec_t, len); }
size_t rs_align_max(void) { return _Alignof(max_align_t); }

rs_rec_t *rs_resize(rs_rec_t *p, int newn, int *err) {
	errno = 0;
	rs_rec_t *q = (rs_rec_t *)realloc(p, (size_t)newn * sizeof(rs_rec_t));
	if (q == NULL) {
		*err = errno;
		return NULL;
	}
	*err = 0;
	return q;
}

static void fill_one(rs_rec_t *r, int i) {
	r->id = (int32_t)i;
	r->flags = (int32_t)((i * 37 + 11) & 0xff);
	r->len = (int32_t)(i % 15);
	r->pad = 0;
	memset(r->name, 0, sizeof(r->name));
	snprintf(r->name, sizeof(r->name), "rec-%03d", i % 1000);
}

void rs_fill(rs_rec_t *p, int from, int to) {
	for (int i = from; i < to; i++)
		fill_one(&p[i], i);
}

int rs_verify(const rs_rec_t *p, int n) {
	char want[16];
	for (int i = 0; i < n; i++) {
		snprintf(want, sizeof(want), "rec-%03d", i % 1000);
		if (p[i].id != (int32_t)i)
			return i;
		if (p[i].flags != (int32_t)((i * 37 + 11) & 0xff))
			return i;
		if (p[i].len != (int32_t)(i % 15))
			return i;
		if (p[i].pad != 0)
			return i;
		if (strcmp(p[i].name, want) != 0)
			return i;
	}
	return -1;
}

uint64_t rs_hash(const rs_rec_t *p, int n) {
	const unsigned char *b = (const unsigned char *)p;
	size_t m = (size_t)n * sizeof(rs_rec_t);
	uint64_t h = UINT64_C(1469598103934665603);
	for (size_t i = 0; i < m; i++) {
		h ^= b[i];
		h *= UINT64_C(1099511628211);
	}
	return h;
}

int rs_name_at(const rs_rec_t *p, int idx, char *out, int cap) {
	if (idx < 0 || cap <= 0)
		return -1;
	size_t n = strnlen(p[idx].name, sizeof(p[idx].name));
	if ((int)n >= cap)
		n = (size_t)cap - 1;
	memcpy(out, p[idx].name, n);
	out[n] = '\0';
	return (int)n;
}

int rs_flags_at(const rs_rec_t *p, int idx) { return (int)p[idx].flags; }
