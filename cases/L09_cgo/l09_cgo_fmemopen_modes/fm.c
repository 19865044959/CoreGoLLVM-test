#define _GNU_SOURCE
#include "fm.h"

#include <errno.h>
#include <stdio.h>

void *fm_open(void *buf, int size, const char *mode, int *err) {
	if (size < 0) {
		*err = EINVAL;
		return NULL;
	}
	errno = 0;
	FILE *f = fmemopen(buf, (size_t)size, mode);
	*err = errno;
	return (void *)f;
}

int fm_put(void *vp, const char *s, int len) {
	if (len <= 0)
		return 0;
	return (int)fwrite(s, 1, (size_t)len, (FILE *)vp);
}

int fm_putc(void *vp, int c) { return fputc(c, (FILE *)vp); }
int fm_flush(void *vp) { return fflush((FILE *)vp); }
int fm_seek(void *vp, long off, int whence) { return fseek((FILE *)vp, off, whence); }
long fm_tell(void *vp) { return ftell((FILE *)vp); }
int fm_getc(void *vp) { return fgetc((FILE *)vp); }

int fm_gets(void *vp, char *out, int cap) {
	FILE *f = (FILE *)vp;
	if (cap <= 0)
		return -2;
	if (fgets(out, cap, f) != NULL)
		return 1;
	return ferror(f) ? -1 : 0;
}

int fm_read(void *vp, char *out, int cap, int *err) {
	if (cap <= 0)
		return 0;
	errno = 0;
	size_t n = fread(out, 1, (size_t)cap, (FILE *)vp);
	*err = errno;
	return (int)n;
}

int fm_eof(void *vp) { return feof((FILE *)vp); }
int fm_error(void *vp) { return ferror((FILE *)vp); }

int fm_close(void *vp, int *err) {
	errno = 0;
	int rc = fclose((FILE *)vp);
	*err = errno;
	return rc;
}
