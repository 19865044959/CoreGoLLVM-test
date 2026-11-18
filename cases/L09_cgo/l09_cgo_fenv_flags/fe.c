#include "fe.h"

#include <fenv.h>
#include <float.h>
#include <math.h>

static fenv_t saved_env;

int fe_mask_invalid(void) { return FE_INVALID; }
int fe_mask_divbyzero(void) { return FE_DIVBYZERO; }
int fe_mask_overflow(void) { return FE_OVERFLOW; }
int fe_mask_underflow(void) { return FE_UNDERFLOW; }
int fe_mask_inexact(void) { return FE_INEXACT; }
int fe_mask_all(void) { return FE_ALL_EXCEPT; }

int fe_round_nearest(void) { return FE_TONEAREST; }
int fe_round_up(void) { return FE_UPWARD; }
int fe_round_down(void) { return FE_DOWNWARD; }
int fe_round_zero(void) { return FE_TOWARDZERO; }

int fe_clear(void) { return feclearexcept(FE_ALL_EXCEPT); }
int fe_test(int mask) { return fetestexcept(mask); }
int fe_raise(int mask) { return feraiseexcept(mask); }
int fe_set_round(int mode) { return fesetround(mode); }
int fe_get_round(void) { return fegetround(); }

void fe_save(void) { fegetenv(&saved_env); }
void fe_restore(void) { fesetenv(&saved_env); }
void fe_default(void) { fesetenv(FE_DFL_ENV); }

double fe_calc(int op, double a, double b) {
	volatile double va = a;
	volatile double vb = b;
	volatile double r = 0.0;
	switch (op) {
	case 0:
		r = va / vb;
		break;
	case 1:
		r = sqrt(va);
		break;
	case 2:
		r = va * vb;
		break;
	case 3:
		r = va + vb;
		break;
	case 4:
		r = va - vb;
		break;
	default:
		r = 0.0;
		break;
	}
	return r;
}

double fe_true_min(void) {
#ifdef DBL_TRUE_MIN
	return DBL_TRUE_MIN;
#else
	return nextafter(0.0, 1.0);
#endif
}

double fe_dbl_max(void) { return DBL_MAX; }
