#ifndef MP_H
#define MP_H

#include <stdint.h>

int mp_round_nearest(void);
int mp_round_up(void);
int mp_round_down(void);
int mp_get_round(void);
int mp_set_round(int mode);

double mp_add(double a, double b);
double mp_mul(double a, double b);
double mp_div(double a, double b);
uint32_t mp_float_bits(float v);
uint64_t mp_double_bits(double v);
uint32_t mp_float_div_bits(float a, float b);
uint64_t mp_double_div_bits(double a, double b);

double mp_ld_probe(void);
double mp_ld_epsilon(void);
int mp_dbl_mant(void);
int mp_flt_mant(void);
int mp_ldbl_mant(void);
int mp_ldbl_size(void);

#endif
