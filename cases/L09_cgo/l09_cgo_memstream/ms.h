#ifndef MS_H
#define MS_H

#include <stddef.h>
#include <stdint.h>

typedef struct mw_s mw_t;
typedef struct mr_s mr_t;

mw_t *mw_open(int *rc);
int mw_write(mw_t *w, const char *s, int n);
int mw_printf(mw_t *w, int32_t v, const char *s);
long mw_tell(mw_t *w);
int mw_seek(mw_t *w, long off);
void mw_flush(mw_t *w);
const char *mw_buf(mw_t *w, size_t *len);
int mw_error(mw_t *w);
int mw_close(mw_t *w, char **buf, size_t *len);
void ms_free(char *p);

mr_t *mr_open(const char *text, int n, int *rc);
int mr_line(mr_t *r, char *out, int cap);
long mr_tell(mr_t *r);
int mr_seek(mr_t *r, long off);
int mr_eof(mr_t *r);
int mr_error(mr_t *r);
void mr_close(mr_t *r);

#endif
