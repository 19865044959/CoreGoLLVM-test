#ifndef L09_ES_H
#define L09_ES_H

#include <stddef.h>

/* stdio 绑定边界：每个函数在内部清零 errno、执行操作后立即把 errno
 * 写入出参，不把 errno 留到后续调用。FILE* 由 C 拥有。 */
void *es_open(const char *path, const char *mode, int *err);
int es_write(void *f, const char *buf, int len, int *err);
int es_read(void *f, char *out, int cap, int *err);
int es_gets(void *f, char *out, int cap);
int es_flush(void *f);
int es_eof(void *f);
int es_error(void *f);
int es_close(void *f, int *err);
long es_tell(void *f);
int es_rewind(void *f);
int es_remove(const char *path, int *err);
int es_rename(const char *oldpath, const char *newpath, int *err);
long es_strtol(const char *s, int base, int *err, int *endoff);

long es_long_max(void);
long es_long_min(void);
int es_enoent(void);
int es_eisdir(void);
int es_ebadf(void);
int es_erange(void);

#endif
