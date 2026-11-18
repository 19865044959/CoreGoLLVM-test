#ifndef EC_H
#define EC_H

#include <stddef.h>

int ec_open_ro(const char *path, int *err);
int ec_write_fd(int fd, const char *buf, int n, int *err);
int ec_read_fd(int fd, void *buf, int n, int *err);
int ec_close_fd(int fd, int *err);
int ec_lseek_fd(int fd, long off, int whence, int *err);
int ec_errno_enoent(void);
int ec_errno_eisdir(void);
int ec_errno_ebadf(void);
int ec_strerror(int err, char *buf, size_t cap);
int ec_seek_set(void);

#endif
