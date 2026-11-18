#include "es.h"

#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>

void *es_open(const char *path, const char *mode, int *err) {
	errno = 0;
	FILE *f = fopen(path, mode);
	*err = errno;
	return (void *)f;
}

int es_write(void *vp, const char *buf, int len, int *err) {
	FILE *f = (FILE *)vp;
	if (len <= 0)
		return 0;
	errno = 0;
	size_t n = fwrite(buf, 1, (size_t)len, f);
	*err = errno;
	return (int)n;
}

int es_read(void *vp, char *out, int cap, int *err) {
	FILE *f = (FILE *)vp;
	if (cap <= 0)
		return 0;
	errno = 0;
	size_t n = fread(out, 1, (size_t)cap, f);
	*err = errno;
	return (int)n;
}

int es_gets(void *vp, char *out, int cap) {
	FILE *f = (FILE *)vp;
	if (cap <= 0)
		return -2;
	errno = 0;
	if (fgets(out, cap, f) != NULL)
		return 1;
	return ferror(f) ? -1 : 0;
}

int es_flush(void *vp) { return fflush((FILE *)vp); }
int es_eof(void *vp) { return feof((FILE *)vp); }
int es_error(void *vp) { return ferror((FILE *)vp); }

int es_close(void *vp, int *err) {
	errno = 0;
	int rc = fclose((FILE *)vp);
	*err = errno;
	return rc;
}

long es_tell(void *vp) { return ftell((FILE *)vp); }
int es_rewind(void *vp) {
	rewind((FILE *)vp);
	return 0;
}

int es_remove(const char *path, int *err) {
	errno = 0;
	int rc = remove(path);
	*err = errno;
	return rc;
}

int es_rename(const char *oldpath, const char *newpath, int *err) {
	errno = 0;
	int rc = rename(oldpath, newpath);
	*err = errno;
	return rc;
}

long es_strtol(const char *s, int base, int *err, int *endoff) {
	char *end = NULL;
	errno = 0;
	long v = strtol(s, &end, base);
	*err = errno;
	*endoff = (int)(end - s);
	return v;
}

long es_long_max(void) { return LONG_MAX; }
long es_long_min(void) { return LONG_MIN; }
int es_enoent(void) { return ENOENT; }
int es_eisdir(void) { return EISDIR; }
int es_ebadf(void) { return EBADF; }
int es_erange(void) { return ERANGE; }
