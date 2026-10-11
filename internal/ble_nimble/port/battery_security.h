#ifndef GOPINE_BATTERY_SECURITY_H
#define GOPINE_BATTERY_SECURITY_H

#include "host/ble_hs.h"

// Match InfiniTime's BatteryInformationService: this one-byte public value is
// readable immediately. InfiniLink uses its first Battery response to finish
// connection setup; phone feature writes retain their separate security checks.
#define GOPINE_BATTERY_FLAGS (BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_NOTIFY)

static inline int battery_read_access(struct ble_gatt_access_ctxt *ctx,
                                      uint8_t level) {
    if (ctx->op != BLE_GATT_ACCESS_OP_READ_CHR) return BLE_ATT_ERR_UNLIKELY;
    return os_mbuf_append(ctx->om, &level, 1) ? BLE_ATT_ERR_INSUFFICIENT_RES : 0;
}

#endif
