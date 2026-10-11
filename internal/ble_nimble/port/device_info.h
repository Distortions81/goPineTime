#ifndef GOPINE_DEVICE_INFO_H
#define GOPINE_DEVICE_INFO_H

#include <stdbool.h>
#include <string.h>
#include "host/ble_hs.h"

// DIS identifies the actual firmware. The classic InfiniLink Home screen
// stays at Connecting until it receives a nonempty Firmware Revision (2A26).
struct device_info {
    char firmware[48];
    unsigned firmware_length;
};

static inline bool device_info_set_version(struct device_info *info,
                                           const uint8_t *version,unsigned size) {
    static const char prefix[]="goPine ";
    const unsigned prefix_size=sizeof(prefix)-1;
    // Versions supplied by the build are printable ASCII. Reject malformed
    // or oversized input instead of exposing a truncated or invalid revision.
    if(!version || !size || size>=sizeof(info->firmware)-prefix_size)return false;
    for(unsigned i=0;i<size;i++)if(version[i]<0x20 || version[i]>0x7e)return false;
    memcpy(info->firmware,prefix,prefix_size);
    memcpy(info->firmware+prefix_size,version,size);
    info->firmware_length=prefix_size+size;
    info->firmware[info->firmware_length]=0;
    return true;
}

static inline int device_info_read(const struct device_info *info,
                                  struct ble_gatt_access_ctxt *ctx,uint16_t uuid) {
    if(ctx->op!=BLE_GATT_ACCESS_OP_READ_CHR)return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    const char *value;
    unsigned size;
    switch(uuid) {
    case 0x2a24:value="PineTime";size=8;break;
    case 0x2a26:value=info->firmware;size=info->firmware_length;break;
    case 0x2a29:value="PINE64";size=6;break;
    default:return BLE_ATT_ERR_UNLIKELY;
    }
    return os_mbuf_append(ctx->om,value,size)?BLE_ATT_ERR_INSUFFICIENT_RES:0;
}

#endif
