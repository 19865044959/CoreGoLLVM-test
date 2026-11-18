#include "strarray.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static char *g_static_names[] = {
	"alpha",
	"",
	"beta-2",
	"hello, cgo",
	"zeta",
	NULL,
};

char **c_static_names(int32_t *n) {
	int32_t c = 0;
	while (g_static_names[c] != NULL) {
		c++;
	}
	*n = c;
	return g_static_names;
}

char **c_alloc_names(int32_t *n) {
	const int32_t count = 12;
	char **v = (char **)calloc((size_t)count + 1, sizeof(char *));
	if (v == NULL) {
		*n = 0;
		return NULL;
	}
	for (int32_t i = 0; i < count; i++) {
		if (i == 3) {
			v[i] = (char *)calloc(1, 1); /* 空串仍是非 NULL 的有效指针 */
		} else if (i == 7) {
			v[i] = (char *)malloc(201);
			if (v[i] != NULL) {
				for (int32_t j = 0; j < 200; j++) {
					v[i][j] = (char)('a' + (i + j) % 26);
				}
				v[i][200] = 0;
			}
		} else {
			char buf[32];
			snprintf(buf, sizeof buf, "item-%02d", i);
			size_t len = strlen(buf);
			v[i] = (char *)malloc(len + 1);
			if (v[i] != NULL) {
				memcpy(v[i], buf, len + 1);
			}
		}
		if (v[i] == NULL) {
			for (int32_t j = 0; j < i; j++) {
				free(v[j]);
			}
			free(v);
			*n = 0;
			return NULL;
		}
	}
	v[count] = NULL;
	*n = count;
	return v;
}

void c_free_names(char **v) {
	if (v == NULL) {
		return;
	}
	for (int32_t i = 0; v[i] != NULL; i++) {
		free(v[i]);
	}
	free(v);
}

int32_t c_strv_count(char **v) {
	int32_t c = 0;
	if (v == NULL) {
		return 0;
	}
	while (v[c] != NULL) {
		c++;
		if (c > 100000) {
			return -1;
		}
	}
	return c;
}

int64_t c_strv_join_len(char **v, int32_t sep) {
	if (v == NULL) {
		return 0;
	}
	int32_t n = 0;
	int64_t total = 0;
	while (v[n] != NULL) {
		total += (int64_t)strlen(v[n]);
		n++;
	}
	if (n > 1) {
		total += (int64_t)(n - 1) * (int64_t)sep;
	}
	return total;
}

char *c_strv_get(char **v, int32_t i) {
	if (v == NULL || i < 0) {
		return NULL;
	}
	int32_t n = c_strv_count(v);
	if (n < 0 || i >= n) {
		return NULL;
	}
	return v[i];
}
