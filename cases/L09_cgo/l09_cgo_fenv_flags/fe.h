#ifndef L09_FE_H
#define L09_FE_H

/* fenv 掩码/舍入模式常量与操作包装；所有状态操作都走 libm/libc。 */
int fe_mask_invalid(void);
int fe_mask_divbyzero(void);
int fe_mask_overflow(void);
int fe_mask_underflow(void);
int fe_mask_inexact(void);
int fe_mask_all(void);

int fe_round_nearest(void);
int fe_round_up(void);
int fe_round_down(void);
int fe_round_zero(void);

int fe_clear(void);
int fe_test(int mask);
int fe_raise(int mask);
int fe_set_round(int mode);
int fe_get_round(void);

void fe_save(void);
void fe_restore(void);
void fe_default(void);

/* op: 0=a/b, 1=sqrt(a), 2=a*b, 3=a+b, 4=a-b；操作数用 volatile 防常量折叠。 */
double fe_calc(int op, double a, double b);
double fe_true_min(void);
double fe_dbl_max(void);

#endif
