#ifndef L09_RX_H
#define L09_RX_H

#include <stdint.h>

typedef struct {
	int32_t s;
	int32_t e;
} rx_span_t;

void *rx_compile(const char *pat, int cflags, int *err);
void rx_free(void *re);

/* 用 REG_STARTEND 在 [start,end) 字节范围内匹配；spans 由 Go 提供，仅在
 * 调用期内写入。返回 regexec 的 rc，同时写入 *nerr。 */
int rx_exec(void *re, const char *text, int len, int start, int end, int eflags,
            rx_span_t *spans, int max, int *nerr);

int rx_flag_startend(void);
int rx_flag_notbol(void);
int rx_flag_noteol(void);
int rx_flag_extended(void);
int rx_nomatch(void);
int rx_eparen(void);

#endif
