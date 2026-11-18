#ifndef L09_FM_H
#define L09_FM_H

/* fmemopen 绑定：缓冲区由调用方（C 堆）提供，FILE* 由 C 拥有。 */
void *fm_open(void *buf, int size, const char *mode, int *err);
int fm_put(void *f, const char *s, int len);
int fm_putc(void *f, int c);
int fm_flush(void *f);
int fm_seek(void *f, long off, int whence);
long fm_tell(void *f);
int fm_getc(void *f);
int fm_gets(void *f, char *out, int cap);
int fm_read(void *f, char *out, int cap, int *err);
int fm_eof(void *f);
int fm_error(void *f);
int fm_close(void *f, int *err);

#endif
