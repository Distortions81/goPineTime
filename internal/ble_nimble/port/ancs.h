#ifndef GOPINE_ANCS_H
#define GOPINE_ANCS_H
#include "host/ble_hs.h"
#include "ancs_mailbox.h"

// Internal output appends a source slot to the 147-byte ANCS record.
#define ANCS_PACKET_SIZE (ANCS_RECORD_SIZE+1)
void gopine_ancs_reset(void);
void gopine_ancs_reset_peer(uint16_t conn);
void gopine_ancs_poll(uint16_t conn, bool trusted);
void gopine_ancs_notify(uint16_t conn, uint16_t attr, struct os_mbuf *om);
int gopine_ancs_take(uint8_t *out, bool trusted);
bool gopine_ancs_updates(void);
uint32_t gopine_ancs_delay(void);
#endif
