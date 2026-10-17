#ifndef GOPINE_BLE_POWER_H
#define GOPINE_BLE_POWER_H

#include "controller/ble_phy.h"
#include "controller/ble_hw.h"

// Call only after controller scheduling has stopped (or before host startup).
static inline void gopine_ble_power_down(void) {
    ble_phy_disable();
    // PHY disable stops RADIO/TIMER0/PPI, but deliberately retains encryption
    // state between connection events. Even ble_ll_reset -> ble_phy_init leaves
    // CCM/AAR ENABLE untouched. This is a full session shutdown: stop CCM and
    // clear its enable and software flag. AAR shares the same STOP/ENABLE
    // registers on nRF52832, so this also disables private-address resolution.
    ble_phy_encrypt_disable();
    ble_hw_rng_stop();
    ble_phy_rfclk_disable();
}

#endif
