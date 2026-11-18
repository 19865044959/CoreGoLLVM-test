#include "mp.h"

#include <fenv.h>
#include <float.h>
#include <stdint.h>
#include <string.h>

/* 舍入模式通过 C 辅助函数切换；运算放在独立函数里，避免被常量折叠。 */

int mp_round_nearest(void) { return FE_TONEAREST; }
int mp_round_up(void) { return FE_UPWARD; }
int mp_round_down(void) { return FE_DOWNWARD; }
int mp_get_round(void) { return fegetround(); }
int mp_set_round(int mode) { return fesetround(mode); }

double mp_add(double a, double b) {
	volatile double r = a + b;
	return r;
}

double mp_mul(double a, double b) {
	volatile double r = a * b;
	return r;
}

double mp_div(double a, double b) {
	volatile double r = a / b;
	return r;
}

uint32_t mp_float_bits(float v) {
	uint32_t u;
	memcpy(&u, &v, sizeof(u));
	return u;
}

uint64_t mp_double_bits(double v) {
	uint64_t u;
	memcpy(&u, &v, sizeof(u));
	return u;
}

uint32_t mp_float_div_bits(float a, float b) {
	float r = a / b;
	uint32_t u;
	memcpy(&u, &r, sizeof(u));
	return u;
}

uint64_t mp_double_div_bits(double a, double b) {
	double r = a / b;
	uint64_t u;
	memcpy(&u, &r, sizeof(u));
	return u;
}

/* (1 + 1e-19) - 1 在 double 中为 0；long double 扩展尾数才能保留。
 * 该探针要求目标平台 long double 至少 64 位尾数（linux/amd64 x87）。 */
double mp_ld_probe(void) {
	volatile long double one = 1.0L;
	volatile long double tiny = 1.0e-19L;
	long double r = (one + tiny) - one;
	return (double)r;
}

double mp_ld_epsilon(void) { return (double)LDBL_EPSILON; }
int mp_dbl_mant(void) { return DBL_MANT_DIG; }
int mp_flt_mant(void) { return FLT_MANT_DIG; }
int mp_ldbl_mant(void) { return LDBL_MANT_DIG; }
int mp_ldbl_size(void) { return (int)sizeof(long double); }
