#ifndef L09_RS_H
#define L09_RS_H

#include <stddef.h>
#include <stdint.h>

/* 含内嵌 char 数组的记录：realloc 搬移时必须逐字节保留前缀。 */
typedef struct {
	int32_t id;
	int32_t flags;
	int32_t len;
	int32_t pad;
	char name[16];
} rs_rec_t;

size_t rs_size(void);
size_t rs_align(void);
size_t rs_off_name(void);
size_t rs_off_len(void);
size_t rs_align_max(void);

/* realloc 包装：失败返回 NULL 并写入 errno，原块保持有效。 */
rs_rec_t *rs_resize(rs_rec_t *p, int newn, int *err);

/* 确定性填充 [from,to)：name="rec-%03d"(i%1000)、flags=(i*37+11)&0xff、len=i%15。 */
void rs_fill(rs_rec_t *p, int from, int to);

/* 逐字段校验；返回 -1 通过，否则返回首个坏下标。 */
int rs_verify(const rs_rec_t *p, int n);

/* 对记录数组原始字节做 FNV-1a 64。 */
uint64_t rs_hash(const rs_rec_t *p, int n);

int rs_name_at(const rs_rec_t *p, int idx, char *out, int cap);
int rs_flags_at(const rs_rec_t *p, int idx);

#endif
