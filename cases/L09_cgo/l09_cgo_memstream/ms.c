#define _GNU_SOURCE
#include "ms.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* FILE* 生命周期完全在 C 侧：Go 只持有不透明结构指针。
 * open_memstream 的 *size 在 fflush/fclose 时更新为当前位置，
 * 因此每次读取缓冲都用 mw_buf 显式 flush 后再取。 */

struct mw_s {
	FILE *fp;
	char *buf;
	size_t len;
};

struct mr_s {
	FILE *fp;
	char *buf;
	size_t len;
};

mw_t *mw_open(int *rc) {
	mw_t *w = (mw_t *)calloc(1, sizeof(*w));
	if (!w) {
		if (rc) *rc = -1;
		return NULL;
	}
	w->fp = open_memstream(&w->buf, &w->len);
	if (!w->fp) {
		free(w);
		if (rc) *rc = -1;
		return NULL;
	}
	if (rc) *rc = 0;
	return w;
}

int mw_write(mw_t *w, const char *s, int n) {
	if (!w || !w->fp || n < 0) return -1;
	return (int)fwrite(s, 1, (size_t)n, w->fp);
}

int mw_printf(mw_t *w, int32_t v, const char *s) {
	if (!w || !w->fp) return -1;
	return fprintf(w->fp, "%d:%s", (int)v, s ? s : "(null)");
}

long mw_tell(mw_t *w) {
	return (w && w->fp) ? ftell(w->fp) : -1;
}

int mw_seek(mw_t *w, long off) {
	return (w && w->fp) ? fseek(w->fp, off, SEEK_SET) : -1;
}

void mw_flush(mw_t *w) {
	if (w && w->fp) fflush(w->fp);
}

const char *mw_buf(mw_t *w, size_t *len) {
	if (!w || !w->fp) {
		if (len) *len = 0;
		return NULL;
	}
	fflush(w->fp);
	if (len) *len = w->len;
	return w->buf;
}

int mw_error(mw_t *w) {
	return (w && w->fp) ? ferror(w->fp) : -1;
}

int mw_close(mw_t *w, char **buf, size_t *len) {
	if (!w) return -1;
	int rc = 0;
	if (w->fp) {
		rc = fclose(w->fp);
		w->fp = NULL;
	}
	if (buf) *buf = w->buf;
	if (len) *len = w->len;
	free(w);
	return rc;
}

void ms_free(char *p) { free(p); }

mr_t *mr_open(const char *text, int n, int *rc) {
	mr_t *r;
	if (n < 0 || (n > 0 && !text)) {
		if (rc) *rc = -1;
		return NULL;
	}
	r = (mr_t *)calloc(1, sizeof(*r));
	if (!r) {
		if (rc) *rc = -1;
		return NULL;
	}
	r->buf = (char *)malloc((size_t)n + 1);
	if (!r->buf) {
		free(r);
		if (rc) *rc = -1;
		return NULL;
	}
	if (n > 0) memcpy(r->buf, text, (size_t)n);
	r->buf[n] = '\0';
	r->len = (size_t)n;
	r->fp = fmemopen(r->buf, r->len, "r");
	if (!r->fp) {
		free(r->buf);
		free(r);
		if (rc) *rc = -1;
		return NULL;
	}
	if (rc) *rc = 0;
	return r;
}

int mr_line(mr_t *r, char *out, int cap) {
	if (!r || !r->fp || !out || cap <= 0) return -1;
	if (!fgets(out, cap, r->fp)) return -1;
	return (int)strlen(out);
}

long mr_tell(mr_t *r) {
	return (r && r->fp) ? ftell(r->fp) : -1;
}

int mr_seek(mr_t *r, long off) {
	return (r && r->fp) ? fseek(r->fp, off, SEEK_SET) : -1;
}

int mr_eof(mr_t *r) {
	return (r && r->fp) ? feof(r->fp) : -1;
}

int mr_error(mr_t *r) {
	return (r && r->fp) ? ferror(r->fp) : -1;
}

void mr_close(mr_t *r) {
	if (!r) return;
	if (r->fp) fclose(r->fp);
	free(r->buf);
	free(r);
}
