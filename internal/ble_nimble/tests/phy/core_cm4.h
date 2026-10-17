#ifndef GOPINE_TEST_CORE_CM4_H
#define GOPINE_TEST_CORE_CM4_H
// Host-only CMSIS surface. Peripheral layouts and bit masks use the real MDK.
#include <stdint.h>
#define __I volatile const
#define __O volatile
#define __IO volatile
static inline void NVIC_SetPriority(int irq, unsigned priority) {(void)irq;(void)priority;}
static inline void NVIC_EnableIRQ(int irq) {(void)irq;}
static inline void NVIC_ClearPendingIRQ(int irq) {(void)irq;}
#endif
