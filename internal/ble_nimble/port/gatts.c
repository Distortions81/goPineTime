// The pinned ble_gatts_start consumes service definitions and reallocates ATT
// memory. Calling it again leaves the old list linked into that reinitialized
// pool. goPine's service table is fixed for the lifetime of the application:
// register once, then preserve its database and CCCD pools across host restarts.
#include "services/gatt/ble_svc_gatt.h"
#define ble_gatts_start gopine_gatts_start_registered
#include "ble_gatts.c"
#undef ble_gatts_start

static bool gopine_services_registered;

int ble_gatts_start(void) {
    if (gopine_services_registered) {
        if (!ble_gatts_mutable()) return BLE_HS_EBUSY;
        // A future dynamic service change needs an explicit database/cache
        // migration; never silently ignore a newly queued service definition.
        if (ble_gatts_num_svc_defs) return BLE_HS_EINVAL;
        return 0;
    }
    int rc = gopine_gatts_start_registered();
    if (!rc) {
        gopine_services_registered = true;
        // Firmware can append services while retaining the phone's bond and
        // old handles. Invalidate its cached database once at boot, after the
        // Service Changed handle is assigned. NimBLE persists this indication
        // for subscribed bonded peers and sends it after encryption restores
        // their CCCD. Ordinary host / mode restarts keep the same database.
        ble_svc_gatt_changed(1, 0xffff);
    }
    return rc;
}
