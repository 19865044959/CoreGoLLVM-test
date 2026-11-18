#define _GNU_SOURCE
#include "rx.h"

#include <regex.h>
#include <stdlib.h>

void *rx_compile(const char *pat, int cflags, int *err) {
	regex_t *re = (regex_t *)malloc(sizeof(regex_t));
	if (re == NULL) {
		*err = -1;
		return NULL;
	}
	int rc = regcomp(re, pat, cflags);
	if (rc != 0) {
		free(re);
		*err = rc;
		return NULL;
	}
	*err = 0;
	return (void *)re;
}

void rx_free(void *vp) {
	regex_t *re = (regex_t *)vp;
	if (re != NULL) {
		regfree(re);
		free(re);
	}
}

int rx_exec(void *vp, const char *text, int len, int start, int end, int eflags,
            rx_span_t *spans, int max, int *nerr) {
	regex_t *re = (regex_t *)vp;
	if (max < 1 || start < 0 || end < start || end > len) {
		*nerr = -2;
		return -2;
	}
	regmatch_t *m = (regmatch_t *)calloc((size_t)max, sizeof(regmatch_t));
	if (m == NULL) {
		*nerr = -3;
		return -3;
	}
	m[0].rm_so = (regoff_t)start;
	m[0].rm_eo = (regoff_t)end;
	int rc = regexec(re, text, (size_t)max, m, eflags | REG_STARTEND);
	if (rc == 0) {
		for (int i = 0; i < max; i++) {
			spans[i].s = (int32_t)m[i].rm_so;
			spans[i].e = (int32_t)m[i].rm_eo;
		}
	}
	free(m);
	*nerr = rc;
	return rc;
}

int rx_flag_startend(void) { return REG_STARTEND; }
int rx_flag_notbol(void) { return REG_NOTBOL; }
int rx_flag_noteol(void) { return REG_NOTEOL; }
int rx_flag_extended(void) { return REG_EXTENDED; }
int rx_nomatch(void) { return REG_NOMATCH; }
int rx_eparen(void) { return REG_EPAREN; }
