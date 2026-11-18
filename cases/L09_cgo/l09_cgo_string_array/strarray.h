#ifndef L09_STRARRAY_H
#define L09_STRARRAY_H

#include <stdint.h>

/* C 字符串数组（char **），NULL 结尾，类似 argv：
 *   - c_static_names 返回静态只读表（不可 free）；
 *   - c_alloc_names 返回 malloc 的指针数组 + 每个字符串单独 malloc；
 *   - c_free_names 只用于 c_alloc_names 的结果。 */
char **c_static_names(int32_t *n);
char **c_alloc_names(int32_t *n);
void c_free_names(char **v);
int32_t c_strv_count(char **v);
int64_t c_strv_join_len(char **v, int32_t sep);
char *c_strv_get(char **v, int32_t i);

#endif
