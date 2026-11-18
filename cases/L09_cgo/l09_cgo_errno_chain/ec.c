#define _GNU_SOURCE
#include "ec.h"

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

/* 每个辅助函数只在自己的调用内设置/读取 errno，并把错误码写入出参，
 * 避免 Go 侧跨 cgo 调用假设 errno 仍是同一线程的旧值。 */

int ec_open_ro(const char *path, int *err) {
	if (err) *err = 0;
	int fd = open(path, O_RDONLY);
	if (fd < 0 && err) *err = errno;
	return fd;
}

int ec_write_fd(int fd, const char *buf, int n, int *err) {
	if (err) *err = 0;
	ssize_t r = write(fd, buf, (size_t)n);
	if (r < 0) {
		if (err) *err = errno;
		return -1;
	}
	return (int)r;
}

int ec_read_fd(int fd, void *buf, int n, int *err) {
	if (err) *err = 0;
	ssize_t r = read(fd, buf, (size_t)n);
	if (r < 0) {
		if (err) *err = errno;
		return -1;
	}
	return (int)r;
}

int ec_close_fd(int fd, int *err) {
	if (err) *err = 0;
	if (close(fd) != 0) {
		if (err) *err = errno;
		return -1;
	}
	return 0;
}

int ec_lseek_fd(int fd, long off, int whence, int *err) {
	if (err) *err = 0;
	off_t r = lseek(fd, (off_t)off, whence);
	if (r < 0) {
		if (err) *err = errno;
		return -1;
	}
	return (int)r;
}

int ec_errno_enoent(void) { return ENOENT; }
int ec_errno_eisdir(void) { return EISDIR; }
int ec_errno_ebadf(void) { return EBADF; }

int ec_strerror(int err, char *buf, size_t cap) {
	if (!buf || cap == 0) return -1;
	const char *s = strerror(err);
	int n = snprintf(buf, cap, "%s", s);
	if (n < 0 || (size_t)n >= cap) return -1;
	return n;
}

int ec_seek_set(void) { return SEEK_SET; }
