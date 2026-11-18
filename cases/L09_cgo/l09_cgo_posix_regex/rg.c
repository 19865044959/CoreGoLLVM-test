#define _GNU_SOURCE
#include "rg.h"

#include <regex.h>
#include <stdint.h>

/* 每次调用独立 regcomp/regexec/regfree；匹配范围同步写入 Go 提供的
 * mr_t 数组，C 不保留任何指针。返回 0=匹配，1=不匹配，-1=编译失败，
 * -2=执行错误。 */
int rg_exec(const char *pat, const char *text, mr_t *out, int max, int *outn) {
	regex_t re;
	regmatch_t pm[32];

	if (outn) *outn = 0;
	if (!pat || !text || !out || max <= 0) return -2;
	if (max > (int)(sizeof(pm) / sizeof(pm[0]))) max = (int)(sizeof(pm) / sizeof(pm[0]));

	int rc = regcomp(&re, pat, REG_EXTENDED);
	if (rc != 0) return -1;

	rc = regexec(&re, text, (size_t)max, pm, 0);
	if (rc == REG_NOMATCH) {
		regfree(&re);
		return 1;
	}
	if (rc != 0) {
		regfree(&re);
		return -2;
	}

	int n = 0;
	for (int i = 0; i < max; i++) {
		if (pm[i].rm_so < 0) break;
		out[n].s = (int32_t)pm[i].rm_so;
		out[n].e = (int32_t)pm[i].rm_eo;
		n++;
	}
	regfree(&re);
	if (outn) *outn = n;
	return 0;
}

int rg_max_ranges(void) { return 32; }
