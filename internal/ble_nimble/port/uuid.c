// Keep the pinned UUID wire operations, replacing only diagnostic formatting.
// The upstream sprintf calls retain libc's general variadic formatter even
// with radio logging disabled. Unreferenced upstream formatting is discarded
// by the firmware linker; the host harness compares both implementations.
#define ble_uuid_to_str gopine_upstream_uuid_to_str
#include "ble_uuid.c"
#undef ble_uuid_to_str

char *ble_uuid_to_str(const ble_uuid_t *uuid, char *dst) {
    static const char hex[] = "0123456789abcdef";
    char *out = dst;
    if (uuid->type == BLE_UUID_TYPE_16 || uuid->type == BLE_UUID_TYPE_32) {
        uint32_t value = uuid->type == BLE_UUID_TYPE_16 ?
            BLE_UUID16(uuid)->value : BLE_UUID32(uuid)->value;
        *out++ = '0'; *out++ = 'x';
        for (int shift = uuid->type == BLE_UUID_TYPE_16 ? 12 : 28; shift >= 0; shift -= 4)
            *out++ = hex[(value >> shift) & 15];
    } else if (uuid->type == BLE_UUID_TYPE_128) {
        const uint8_t *bytes = BLE_UUID128(uuid)->value;
        for (int i = 15; i >= 0; i--) {
            *out++ = hex[bytes[i] >> 4];
            *out++ = hex[bytes[i] & 15];
            if (i == 12 || i == 10 || i == 8 || i == 6) *out++ = '-';
        }
    }
    *out = '\0';
    return dst;
}
