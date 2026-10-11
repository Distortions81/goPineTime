// goPine's cooperative CTS bridge. NimBLE callbacks only copy bounded values;
// the Go UI validates them outside interrupt context. Automatic clock writes
// require an authenticated bonded connection; one-shot sync asks for approval.
#include <string.h>
#include <nrf.h>
#include "nimble/nimble_port.h"
#include "nimble/ble_hci_trans.h"
#include "host/ble_hs.h"
#include "host/ble_hs_id.h"
#include "host/ble_sm.h"
#include "services/gap/ble_svc_gap.h"
#include "services/gatt/ble_svc_gatt.h"
#include "controller/ble_phy.h"
#include "controller/ble_ll.h"
#include "controller/ble_ll_hci.h"
#include "controller/ble_hw.h"
#include "weather_mailbox.h"
#include "music_mailbox.h"
#include "notification_mailbox.h"
#include "update_mailbox.h"
#include "update_links.h"
#include "update_tuning.h"
#include "battery_security.h"
#include "device_info.h"
#include "phone_time.h"
#include "ancs.h"

extern int ble_ll_hci_cmd_rx(uint8_t *, void *);
extern int ble_ll_hci_acl_rx(struct os_mbuf *, void *);
extern void ble_gap_reset_state(int reason);

static bool initialized, synced, window, pending;
static uint16_t connection = BLE_HS_CONN_HANDLE_NONE;
static uint8_t address_type, incoming[10], incoming_len, current[10], battery_level;
static uint16_t battery_handle;
static struct device_info device_information;
static uint32_t received_at, expires_at;
static uint32_t disconnect_after;
static uint32_t fast_until;
static int failure;
static int init_failure;
static bool host_up, stopping, stop_complete;
static bool lifecycle_changed;
static bool weather_window, phone_window, unbounded, slow_advertising;
static uint16_t music_handle;
static uint32_t peer_generation;
struct phone_payload {
    struct music_mailbox music;
    struct weather_mailbox weather;
    struct notification_mailbox notification;
    struct phone_time_mailbox time;
};
// Feature payloads belong to exactly one foreground radio mode. Keep link
// metadata outside this union so late GAP events cannot interpret OTA bytes
// as a phone handle. Mode changes occur only after the previous host stops.
static union {
    struct phone_payload phone[2];
    struct weather_mailbox weather;
    struct update_mailbox update;
} mode_payload;
#define weather_incoming mode_payload.weather
#define update_incoming mode_payload.update
struct phone_peer {
    uint16_t handle;
    uint32_t generation,security_retry_at,close_retry_at;
    bool security_pending,music_subscribed,battery_subscribed,close_requested,terminating;
    bool battery_snapshot_pending,battery_snapshot_sent;
};
static struct phone_peer phone_peers[2];
static struct phone_payload *phone_data(const struct phone_peer *peer) {
    return &mode_payload.phone[peer-phone_peers];
}
static bool music_snapshot_dirty,phone_advertising_pending;
static unsigned time_cursor,weather_cursor,notification_cursor;
static struct phone_peer *phone_peer_find(uint16_t handle) {
    if(handle==BLE_HS_CONN_HANDLE_NONE)return NULL;
    for(unsigned i=0;i<2;i++)if(phone_peers[i].handle==handle)return &phone_peers[i];
    return NULL;
}
static void phone_peers_reset(void) {
    memset(phone_peers,0,sizeof(phone_peers));
    for(unsigned i=0;i<2;i++)phone_peers[i].handle=BLE_HS_CONN_HANDLE_NONE;
    connection=BLE_HS_CONN_HANDLE_NONE;
    music_snapshot_dirty=true;phone_advertising_pending=false;
    time_cursor=weather_cursor=notification_cursor=0;
}
static struct phone_peer *phone_peer_add(uint16_t handle) {
    for(unsigned i=0;i<2;i++) {
        struct phone_peer *peer=&phone_peers[i];
        if(peer->handle!=BLE_HS_CONN_HANDLE_NONE)continue;
        if(phone_window)memset(phone_data(peer),0,sizeof(*phone_data(peer)));
        memset(peer,0,sizeof(*peer));peer->handle=handle;
        if(!++peer_generation)++peer_generation;
        peer->generation=peer_generation;
        if(phone_window)music_clear(&phone_data(peer)->music);
        if(connection==BLE_HS_CONN_HANDLE_NONE) {
            connection=handle;music_snapshot_dirty=true;
        }
        return peer;
    }
    return NULL;
}
static void phone_peer_close(struct phone_peer *peer) {
    if(!peer->close_requested)peer->close_retry_at=ble_npl_time_get();
    peer->close_requested=true;peer->security_pending=false;
}
unsigned gopine_ble_peer_count(void) {
    if(!window || !phone_window)return 0;
    unsigned count=0;
    for(unsigned i=0;i<2;i++)if(phone_peers[i].handle!=BLE_HS_CONN_HANDLE_NONE)count++;
    return count;
}
unsigned gopine_ble_trusted_peer_count(void) {
    if(!window || !phone_window)return 0;
    unsigned count=0;
    for(unsigned i=0;i<2;i++)if(phone_link_ready(phone_peers[i].handle))count++;
    return count;
}
static bool update_window;
static uint32_t pairing_code; // Code + 1; zero means no active prompt.
static bool pairing_io_pending;
static uint16_t pairing_connection=BLE_HS_CONN_HANDLE_NONE;
static uint16_t last_security_status, last_disconnect_reason;
static uint16_t pairing_progress;
// Bounded non-secret trace survives phone retries and an OTA-mode handoff.
// Records: uptime ms (LE32), event, phase/round low byte, result (LE16).
static uint8_t pairing_trace[196] = {1};
static unsigned trace_count;
static void trace_pairing(uint8_t event,uint16_t result) {
    if(trace_count==24) {
        memmove(pairing_trace+4,pairing_trace+12,23*8);
        trace_count--;
    }
    uint8_t *record=pairing_trace+4+trace_count++*8;
    uint32_t now=ble_npl_time_get();
    for(unsigned i=0;i<4;i++)record[i]=(uint8_t)(now>>(i*8));
    record[4]=event;record[5]=(uint8_t)pairing_progress;
    record[6]=(uint8_t)result;record[7]=(uint8_t)(result>>8);
    pairing_trace[1]=(uint8_t)trace_count;
    pairing_trace[2]=(uint8_t)(pairing_progress>>8);
}
void gopine_ble_trace_security(uint8_t event,uint16_t status) {
    // Only these non-secret SM outcomes are accepted across the port boundary.
    // OTA lookup traffic must not replace the retained Phone failure trace.
    if(phone_window && (event==16 || event==17)) trace_pairing(event,status);
}
// Security metadata only: no key bytes or peer identifiers. Distinguish a
// stored record from a usable authenticated bond, and this peer from another.
static uint8_t trace_key_flags(const struct ble_store_value_sec *key) {
    return 1 | (key->ltk_present?2:0) | (key->authenticated?4:0) |
           (key->sc?8:0) | (key->key_size==16?16:0);
}
static uint16_t trace_bond_flags(const ble_addr_t *peer) {
    struct ble_store_key_sec key={.peer_addr=*peer};
    struct ble_store_value_sec value;
    uint16_t flags=0;
    if(!ble_store_read_our_sec(&key,&value))flags=trace_key_flags(&value);
    if(!ble_store_read_peer_sec(&key,&value))flags|=(uint16_t)trace_key_flags(&value)<<8;
    return flags;
}
extern uint16_t gopine_sm_progress(uint16_t conn_handle);
extern void gopine_bond_init(void);
extern bool gopine_bond_prepare(uint8_t battery);
extern void gopine_bond_poll(bool stopped);
extern int gopine_bond_status(void);
extern bool gopine_bond_exists(void);
extern bool gopine_bond_peer_saved(uint16_t conn);
extern bool gopine_bond_can_pair(uint16_t conn);
extern uint32_t gopine_bond_delay(void);
extern bool gopine_bond_forget(void);
extern int gopine_ble_restore_privacy(void);
static uint8_t update_status[16];
static uint16_t update_handle;
static struct update_links update_links;

static void update_notify(void) {
    if(!window || !update_window)return;
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++) {
        struct update_link *link=&update_links.entries[i];
        if(link->handle==BLE_HS_CONN_HANDLE_NONE || !link->subscribed || !link->dirty ||
           link->terminating || link->close_requested)continue;
        if(update_links.owner!=BLE_HS_CONN_HANDLE_NONE && update_links.owner!=link->handle) {
            link->dirty=false;continue;
        }
        // A snapshot acknowledges only bytes the Go receiver has read back.
        // MTU is a property of this recipient, not of the most recent peer.
        uint8_t status[16];memcpy(status,update_status,sizeof(status));
        status[3]=(uint8_t)(ble_att_mtu(link->handle)-3);
        struct os_mbuf *om=ble_hs_mbuf_from_flat(status,sizeof(status));
        if(om && !ble_gattc_notify_custom(link->handle,update_handle,om))link->dirty=false;
    }
}

static struct ble_hs_stop_listener stop_listener;

static bool expired(void) {
    return !unbounded && (int32_t)(ble_npl_time_get()-expires_at)>=0;
}
static void battery_snapshot_notify(void) {
    if(!window || !phone_window || expired())return;
    // Classic InfiniLink publishes Battery changes, but not its firmware
    // field. Send the current sample once after the firmware read response
    // has been queued by the host pump, so the companion can redraw Home.
    for(unsigned i=0;i<2;i++) {
        struct phone_peer *peer=&phone_peers[i];
        if(peer->handle==BLE_HS_CONN_HANDLE_NONE || !peer->battery_snapshot_pending ||
           !peer->battery_subscribed || peer->close_requested)continue;
        struct os_mbuf *om=ble_hs_mbuf_from_flat(&battery_level,1);
        if(om && !ble_gattc_notify_custom(peer->handle,battery_handle,om)) {
            peer->battery_snapshot_pending=false;
            peer->battery_snapshot_sent=true;
        }
    }
}
static void stopped(int status, void *arg) {
    (void)arg;
    if (status) failure = status;
    stop_complete = true;
}
static void finish_stop(void) {
    // The completion callback can run inside the host's disconnect dispatch.
    // Reset only after that dispatch and all nested controller work unwind.
    if (!stop_complete) return;
    stop_complete = false;
    // Also covers an out-of-range peer which never acknowledged disconnect.
    ble_ll_reset();
    // A timed-out peer may still have host-side connection records. Clear
    // them through the same GAP cleanup used after a controller reset.
    ble_gap_reset_state(BLE_HS_ECONTROLLER);
    ble_phy_disable();
    ble_hw_rng_stop();
    ble_phy_rfclk_disable();
    connection = BLE_HS_CONN_HANDLE_NONE;
    synced = host_up = stopping = false;
    lifecycle_changed = true;
}
void gopine_ble_stop(void);

static int time_access(uint16_t conn, uint16_t attr,
                       struct ble_gatt_access_ctxt *ctx, void *arg) {
    (void)attr; (void)arg;
    if (ctx->op == BLE_GATT_ACCESS_OP_READ_CHR) {
        return os_mbuf_append(ctx->om, current, 10) ? BLE_ATT_ERR_INSUFFICIENT_RES : 0;
    }
    if (!window || update_window || expired())
        return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    if (phone_window) {
        struct phone_peer *peer=phone_peer_find(conn);
        if(!peer)return BLE_ATT_ERR_UNLIKELY;
        uint8_t value[10];
        unsigned len = OS_MBUF_PKTLEN(ctx->om);
        if (len < 9 || len > sizeof(value)) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
        if (os_mbuf_copydata(ctx->om, 0, len, value)) return BLE_ATT_ERR_UNLIKELY;
        return phone_time_receive(&phone_data(peer)->time,conn,peer->generation,
                                  value, len, ble_npl_time_get());
    }
    if (pending) return BLE_ATT_ERR_INSUFFICIENT_RES;
    uint16_t len = OS_MBUF_PKTLEN(ctx->om);
    if (len < 9 || len > 10) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    if (os_mbuf_copydata(ctx->om, 0, len, incoming)) return BLE_ATT_ERR_UNLIKELY;
    // Companions commonly send CTS before weather. A transport ACK must not
    // close this window or silently apply time without on-watch approval.
    if (weather_window) return 0;
    incoming_len = len;
    received_at = ble_npl_time_get();
    pending = true;
    return 0; // Transport receipt only, not approval of a clock change.
}

static int battery_access(uint16_t conn, uint16_t attr,
                          struct ble_gatt_access_ctxt *ctx, void *arg) {
    (void)conn; (void)attr; (void)arg;
    return battery_read_access(ctx,battery_level);
}

void gopine_ble_update_battery(uint8_t value) {
    if(value>100) value=100;
    if(value==battery_level) return;
    battery_level=value;
    // Uses the last UI sample, never an additional ADC read or sleep poll.
    // NimBLE coalesces updates and only notifies subscribed peers.
    if(window)
        ble_gatts_chr_updated(battery_handle);
}

static int weather_access(uint16_t conn, uint16_t attr,
                          struct ble_gatt_access_ctxt *ctx, void *arg) {
    (void)attr; (void)arg;
    if (ctx->op != BLE_GATT_ACCESS_OP_WRITE_CHR || !window || !(weather_window || phone_window) || expired()) return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    struct weather_mailbox *mailbox=&weather_incoming;
    if (phone_window) {
        struct phone_peer *peer=phone_peer_find(conn);
        if(!peer)return BLE_ATT_ERR_UNLIKELY;
        mailbox=&phone_data(peer)->weather;
        int rc = phone_link_permission(conn);
        if (rc) return rc;
    }
    uint16_t len = OS_MBUF_PKTLEN(ctx->om);
    uint8_t data[53];
    if (len > sizeof(data)) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    if (os_mbuf_copydata(ctx->om, 0, len, data)) return BLE_ATT_ERR_UNLIKELY;
    if (!weather_store(mailbox,data,len)) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    disconnect_after = ble_npl_time_get() + 200; // Preserve the last ATT ACK.
    return 0;
}

static const ble_uuid128_t weather_uuid = BLE_UUID128_INIT(
    0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,0x00,0x00,0x05,0x00);
static const ble_uuid128_t weather_data_uuid = BLE_UUID128_INIT(
    0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,0x01,0x00,0x05,0x00);

// Protocol declarations, independently implemented from InfiniTime's UUIDs.
#define MUSIC_UUID(id) BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,id,0,0,0)
static const ble_uuid128_t music_uuids[] = {
    MUSIC_UUID(0), MUSIC_UUID(1), MUSIC_UUID(2), MUSIC_UUID(3),
    MUSIC_UUID(4), MUSIC_UUID(5), MUSIC_UUID(6), MUSIC_UUID(7),
    MUSIC_UUID(8), MUSIC_UUID(9), MUSIC_UUID(10), MUSIC_UUID(11), MUSIC_UUID(12)
};
static int music_access(uint16_t conn, uint16_t attr,
                        struct ble_gatt_access_ctxt *ctx, void *arg) {
    (void)attr;
    unsigned id=(uintptr_t)arg, len;
    struct phone_peer *peer=phone_window?phone_peer_find(conn):NULL;
    if(!peer) {
        // Foreground modes expose the same public characteristic schema, but
        // have no phone metadata. Keep their overlaid payloads inaccessible.
        static const uint8_t zero[4]={0};
        if(ctx->op!=BLE_GATT_ACCESS_OP_READ_CHR)return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
        if(id<2 || id>12)return BLE_ATT_ERR_UNLIKELY;
        unsigned size=(id>=3 && id<=5)?0:((id>=6 && id<=10)?4:1);
        return os_mbuf_append(ctx->om,zero,size)?BLE_ATT_ERR_INSUFFICIENT_RES:0;
    }
    uint8_t *field=music_field(&phone_data(peer)->music,id,&len);
    if(!field) return BLE_ATT_ERR_UNLIKELY;
    if(ctx->op==BLE_GATT_ACCESS_OP_READ_CHR)
        return os_mbuf_append(ctx->om,field,len)?BLE_ATT_ERR_INSUFFICIENT_RES:0;
    if(ctx->op!=BLE_GATT_ACCESS_OP_WRITE_CHR || !window || !phone_window || expired() || !peer)
        return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    int rc = phone_link_permission(conn);
    if(rc) return rc;
    unsigned size=OS_MBUF_PKTLEN(ctx->om);
    uint8_t data[40];
    if(os_mbuf_copydata(ctx->om,0,size>40?40:size,data)) return BLE_ATT_ERR_UNLIKELY;
    if(!music_store(&phone_data(peer)->music,id,data,size)) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    if(id<=4) {
        if(connection!=conn || phone_data(peer)->music.dirty)music_snapshot_dirty=true;
        connection=conn;
    }
    disconnect_after=ble_npl_time_get()+200;
    return 0;
}
#define MUSIC_CHAR(id) {.uuid=&music_uuids[id].u, .access_cb=music_access, .arg=(void *)(uintptr_t)id, .flags=BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_WRITE}

static int notification_access(uint16_t conn, uint16_t attr,
                               struct ble_gatt_access_ctxt *ctx, void *arg) {
    (void)attr; (void)arg;
    if(ctx->op!=BLE_GATT_ACCESS_OP_WRITE_CHR || !window || !phone_window || expired())
        return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    int rc = phone_link_permission(conn);
    if(rc) return rc;
    struct phone_peer *peer=phone_peer_find(conn);
    if(!peer)return BLE_ATT_ERR_UNLIKELY;
    unsigned size=OS_MBUF_PKTLEN(ctx->om);
    if(size<4) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    uint8_t data[NOTIFICATION_PACKET_SIZE];
    unsigned used=size>sizeof(data)?sizeof(data):size;
    if(os_mbuf_copydata(ctx->om,0,used,data)) return BLE_ATT_ERR_UNLIKELY;
    if(size>used) memcpy(data+used-3,"...",3);
    if(!notification_store(&phone_data(peer)->notification,data,used)) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    disconnect_after=ble_npl_time_get()+200;
    return 0;
}

// goPine foreground update service; no command can activate an image remotely.
static const ble_uuid128_t update_uuids[]={
    BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,0,0,6,0),
    BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,1,0,6,0),
    BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,2,0,6,0),
    BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,3,0,6,0)
};
static int update_access(uint16_t conn,uint16_t attr,struct ble_gatt_access_ctxt *ctx,void *arg) {
    (void)attr;
    if(ctx->op==BLE_GATT_ACCESS_OP_READ_CHR && (uintptr_t)arg==1) {
        // Revision, outstanding packet window, DATA Write Command support,
        // reserved. STATUS remains the protocol-2 durable acknowledgement.
        const uint8_t capabilities[]={1,UPDATE_PACKET_WINDOW,1,0};
        return os_mbuf_append(ctx->om,capabilities,sizeof(capabilities))?BLE_ATT_ERR_INSUFFICIENT_RES:0;
    }
    if(ctx->op==BLE_GATT_ACCESS_OP_READ_CHR && (uintptr_t)arg==3) {
        uint8_t status[16];memcpy(status,update_status,sizeof(status));
        status[3]=conn==BLE_HS_CONN_HANDLE_NONE?0:(uint8_t)(ble_att_mtu(conn)-3);
        return os_mbuf_append(ctx->om,status,sizeof(status))?BLE_ATT_ERR_INSUFFICIENT_RES:0;
    }
    struct update_link *link=update_links_find(&update_links,conn);
    if(ctx->op!=BLE_GATT_ACCESS_OP_WRITE_CHR || !window || !update_window || expired() ||
       !link || !link->subscribed || link->terminating || link->close_requested ||
       (update_links.owner!=BLE_HS_CONN_HANDLE_NONE && update_links.owner!=conn))
        return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    unsigned n=OS_MBUF_PKTLEN(ctx->om);
    uint8_t data[244];
    if(n>sizeof(data)) return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    if(os_mbuf_copydata(ctx->om,0,n,data))return BLE_ATT_ERR_UNLIKELY;
    if(update_links.owner==BLE_HS_CONN_HANDLE_NONE &&
       !update_claimable(update_status,(uintptr_t)arg,data,n))return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    int rc=update_store(&update_incoming,update_status,(uintptr_t)arg,data,n);
    if(rc==-2)return BLE_ATT_ERR_WRITE_NOT_PERMITTED;
    if(rc<0)return BLE_ATT_ERR_INVALID_ATTR_VALUE_LEN;
    if(!rc)return BLE_ATT_ERR_INSUFFICIENT_RES;
    if(update_links.owner==BLE_HS_CONN_HANDLE_NONE) {
        update_links.owner=conn;
        update_links.advertising_pending=true;
    }
    return 0;
}

void gopine_ble_set_firmware_version(const uint8_t *version,unsigned size) {
    // The Go bridge supplies its authoritative version before radio startup.
    // Keep a connection's public metadata stable throughout its lifetime.
    if(!host_up && !stopping)device_info_set_version(&device_information,version,size);
}
static int device_info_access(uint16_t conn,uint16_t attr,
                              struct ble_gatt_access_ctxt *ctx,void *arg) {
    (void)attr;
    uint16_t uuid=(uint16_t)(uintptr_t)arg;
    // The pinned ATT host prepares this one-byte response before invoking a
    // normal Read Request. Exclude local reads and Read Blob callbacks, whose
    // offset validation happens only after this callback returns.
    uint8_t opcode=0;
    bool normal_read=ctx->op==BLE_GATT_ACCESS_OP_READ_CHR &&
        OS_MBUF_PKTLEN(ctx->om)==1 && !os_mbuf_copydata(ctx->om,0,1,&opcode) &&
        opcode==BLE_ATT_OP_READ_RSP;
    int rc=device_info_read(&device_information,ctx,uuid);
    struct phone_peer *peer=phone_peer_find(conn);
    if(!rc && uuid==0x2a26 && device_information.firmware_length && normal_read &&
       window && phone_window && !expired() && peer &&
       peer->battery_subscribed && !peer->battery_snapshot_sent)
        peer->battery_snapshot_pending=true;
    return rc;
}

static const ble_uuid128_t pairing_trace_service=BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,0,0,7,0);
static const ble_uuid128_t pairing_trace_characteristic=BLE_UUID128_INIT(0xd0,0x42,0x19,0x3a,0x3b,0x43,0x23,0x8e,0xfe,0x48,0xfc,0x78,1,0,7,0);
static int pairing_trace_access(uint16_t conn,uint16_t attr,struct ble_gatt_access_ctxt *ctx,void *arg) {
    (void)conn;(void)attr;(void)arg;
    if(ctx->op!=BLE_GATT_ACCESS_OP_READ_CHR)return BLE_ATT_ERR_READ_NOT_PERMITTED;
    // No PIN, key, nonce, address or other phone data is exposed here.
    pairing_trace[3]=(uint8_t)trace_bond_flags(BLE_ADDR_ANY);
    return os_mbuf_append(ctx->om,pairing_trace,4+trace_count*8)?BLE_ATT_ERR_INSUFFICIENT_RES:0;
}
// Definitions live in flash; NimBLE writes only through the val_handle pointers.
// The upstream UUID declaration macro creates mutable compound literals.
#define GATT_UUID16(value) ((const ble_uuid_t *)&(const ble_uuid16_t)BLE_UUID16_INIT(value))
static const struct ble_gatt_svc_def services[] = {
    {.type=BLE_GATT_SVC_TYPE_PRIMARY,.uuid=&pairing_trace_service.u,
     .characteristics=(const struct ble_gatt_chr_def[]){
         {.uuid=&pairing_trace_characteristic.u,.access_cb=pairing_trace_access,.flags=BLE_GATT_CHR_F_READ},{0}}},
    {.type=BLE_GATT_SVC_TYPE_PRIMARY,.uuid=&update_uuids[0].u,
     .characteristics=(const struct ble_gatt_chr_def[]){
         {.uuid=&update_uuids[1].u,.access_cb=update_access,.arg=(void*)1,.flags=BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_WRITE},
         {.uuid=&update_uuids[2].u,.access_cb=update_access,.arg=(void*)2,.flags=BLE_GATT_CHR_F_WRITE | BLE_GATT_CHR_F_WRITE_NO_RSP},
         {.uuid=&update_uuids[3].u,.access_cb=update_access,.arg=(void*)3,.flags=BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_NOTIFY,.val_handle=&update_handle}, {0}}},
    {.type=BLE_GATT_SVC_TYPE_PRIMARY, .uuid=GATT_UUID16(0x1811),
     .characteristics=(const struct ble_gatt_chr_def[]) {
         {.uuid=GATT_UUID16(0x2a46), .access_cb=notification_access,
          .flags=BLE_GATT_CHR_F_WRITE}, {0}}},
    {.type = BLE_GATT_SVC_TYPE_PRIMARY, .uuid = GATT_UUID16(0x1805),
     .characteristics = (const struct ble_gatt_chr_def[]) {
         {.uuid = GATT_UUID16(0x2a2b), .access_cb = time_access,
          .flags = BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_WRITE},
         {0}}},
    {.type = BLE_GATT_SVC_TYPE_PRIMARY, .uuid = GATT_UUID16(0x180f),
     .characteristics = (const struct ble_gatt_chr_def[]) {
         {.uuid = GATT_UUID16(0x2a19), .access_cb = battery_access,
          .flags = GOPINE_BATTERY_FLAGS,
          .val_handle = &battery_handle},
         {0}}},
    {.type = BLE_GATT_SVC_TYPE_PRIMARY, .uuid = &weather_uuid.u,
     .characteristics = (const struct ble_gatt_chr_def[]) {
         {.uuid = &weather_data_uuid.u, .access_cb = weather_access,
          .flags = BLE_GATT_CHR_F_WRITE},
         {0}}},
    {.type=BLE_GATT_SVC_TYPE_PRIMARY, .uuid=&music_uuids[0].u,
     .characteristics=(const struct ble_gatt_chr_def[]) {
         {.uuid=&music_uuids[1].u, .access_cb=music_access,
          .flags=BLE_GATT_CHR_F_NOTIFY, .val_handle=&music_handle},
         MUSIC_CHAR(2), MUSIC_CHAR(3), MUSIC_CHAR(4), MUSIC_CHAR(5),
         MUSIC_CHAR(6), MUSIC_CHAR(7), MUSIC_CHAR(8), MUSIC_CHAR(9),
         MUSIC_CHAR(10), MUSIC_CHAR(11), MUSIC_CHAR(12), {0}}},
    // Append new services so existing bonded clients retain their old handles.
    {.type=BLE_GATT_SVC_TYPE_PRIMARY,.uuid=GATT_UUID16(0x180a),
     .characteristics=(const struct ble_gatt_chr_def[]){
         {.uuid=GATT_UUID16(0x2a24),.access_cb=device_info_access,.arg=(void*)0x2a24,.flags=BLE_GATT_CHR_F_READ},
         {.uuid=GATT_UUID16(0x2a26),.access_cb=device_info_access,.arg=(void*)0x2a26,.flags=BLE_GATT_CHR_F_READ},
         {.uuid=GATT_UUID16(0x2a29),.access_cb=device_info_access,.arg=(void*)0x2a29,.flags=BLE_GATT_CHR_F_READ},{0}}},
    {0}
};

#undef GATT_UUID16

static void advertise(void);
static int update_gap_event(struct ble_gap_event *event) {
    struct update_link *link;
    switch(event->type) {
    case BLE_GAP_EVENT_CONNECT:
        if(!event->connect.status) {
            if(update_links_find(&update_links,event->connect.conn_handle))break;
            if(!update_links_add(&update_links,event->connect.conn_handle,ble_npl_time_get()))
                failure=BLE_HS_ENOMEM;
        } else update_links.advertising_pending=true;
        lifecycle_changed=true;
        break;
    case BLE_GAP_EVENT_DISCONNECT:
        link=update_links_find(&update_links,event->disconnect.conn.conn_handle);
        if(!link)break;
        if(update_links.owner==link->handle) {
            update_links.owner=BLE_HS_CONN_HANDLE_NONE;
            // A queued but unconsumed packet was not acknowledged by Go.
            // The receiver and durable offset remain available to a resume.
            memset(&update_incoming,0,sizeof(update_incoming));
            for(unsigned i=0;i<UPDATE_LINK_COUNT;i++)
                update_links.entries[i].dirty=update_links.entries[i].subscribed;
        }
        memset(link,0,sizeof(*link));link->handle=BLE_HS_CONN_HANDLE_NONE;
        update_links.advertising_pending=true;lifecycle_changed=true;
        break;
    case BLE_GAP_EVENT_SUBSCRIBE:
        link=update_links_find(&update_links,event->subscribe.conn_handle);
        if(link && event->subscribe.attr_handle==update_handle) {
            link->subscribed=event->subscribe.cur_notify;
            link->dirty=link->subscribed;
        }
        break;
    case BLE_GAP_EVENT_PASSKEY_ACTION:
        // Updating never pairs a new peer or replaces an existing phone key.
        // Defer termination until the SMP callback has unwound.
        link=update_links_find(&update_links,event->passkey.conn_handle);
        if(link){link->close_requested=true;link->deadline=ble_npl_time_get();}
        break;
    case BLE_GAP_EVENT_REPEAT_PAIRING:
        return BLE_GAP_REPEAT_PAIRING_IGNORE;
    case BLE_GAP_EVENT_TERM_FAILURE:
        link=update_links_find(&update_links,event->term_failure.conn_handle);
        if(link) {
            link->terminating=false;
            link->close_requested=true;
            link->deadline=ble_npl_time_get()+100;
        }
        break;
    case BLE_GAP_EVENT_ADV_COMPLETE:
        update_links.advertising_pending=true;
        break;
    default: break; // Encryption or cached phone CCCDs cannot alter OTA ownership.
    }
    return 0;
}
static int gap_event(struct ble_gap_event *event, void *arg) {
    (void)arg;
    if(update_window)return update_gap_event(event);
    struct phone_peer *peer;
    switch(event->type) {
    case BLE_GAP_EVENT_PASSKEY_ACTION:
        peer=phone_peer_find(event->passkey.conn_handle);
        if(!peer)break;
        trace_pairing(4,event->passkey.params.action);
        if(event->passkey.params.action!=BLE_SM_IOACT_DISP || !window ||
           !gopine_bond_can_pair(peer->handle) ||
           (pairing_connection!=BLE_HS_CONN_HANDLE_NONE && pairing_connection!=peer->handle)) {
            phone_peer_close(peer);break;
        }
        // Only one SMP procedure can own the on-watch PIN prompt at a time.
        pairing_connection=peer->handle;pairing_io_pending=true;
        break;
    case BLE_GAP_EVENT_ENC_CHANGE:
        peer=phone_peer_find(event->enc_change.conn_handle);
        if(!peer)break;
        if(phone_window) {
            last_security_status=event->enc_change.status;
            if(!event->enc_change.status && phone_link_ready(peer->handle)) {
                last_disconnect_reason=0;
                if(pairing_connection==peer->handle || pairing_connection==BLE_HS_CONN_HANDLE_NONE)
                    pairing_progress=6u<<8;
            }
            trace_pairing(6,event->enc_change.status);
            struct ble_gap_conn_desc desc;
            if(!ble_gap_conn_find(peer->handle,&desc))
                trace_pairing(13,(desc.sec_state.key_size<<8) |
                    (desc.sec_state.encrypted?1:0) | (desc.sec_state.authenticated?2:0) |
                    (desc.sec_state.bonded?4:0));
        }
        if(pairing_connection==peer->handle) {
            pairing_code=0;pairing_io_pending=false;pairing_connection=BLE_HS_CONN_HANDLE_NONE;
        }
        peer->security_pending=false;lifecycle_changed=true;
        if(!phone_window)break;
        phone_data(peer)->music.dirty=true;
        if(connection==peer->handle)music_snapshot_dirty=true;
        if(event->enc_change.status || !phone_link_ready(peer->handle)) {
            gopine_ancs_reset_peer(peer->handle);
            phone_time_clear(&phone_data(peer)->time);
            music_clear(&phone_data(peer)->music);
            memset(&phone_data(peer)->weather,0,sizeof(phone_data(peer)->weather));
            memset(&phone_data(peer)->notification,0,sizeof(phone_data(peer)->notification));
        }
        break;
    case BLE_GAP_EVENT_REPEAT_PAIRING:
        // A new phone uses the second slot; an existing identity is never
        // silently replaced by another pairing request.
        trace_pairing(11,BLE_HS_EALREADY);
        return BLE_GAP_REPEAT_PAIRING_IGNORE;
    case BLE_GAP_EVENT_CONNECT:
        if(phone_window)trace_pairing(2,event->connect.status);
        if(!event->connect.status) {
            if(!window || phone_peer_find(event->connect.conn_handle))break;
            peer=phone_peer_add(event->connect.conn_handle);
            if(!peer){failure=BLE_HS_ENOMEM;break;}
            peer->security_pending=phone_window && gopine_bond_peer_saved(peer->handle);
            peer->security_retry_at=ble_npl_time_get();
            if(phone_window) {
                struct ble_gap_conn_desc desc;
                if(!ble_gap_conn_find(peer->handle,&desc))
                    trace_pairing(15,trace_bond_flags(&desc.peer_id_addr));
            }
            lifecycle_changed=true;
        } else if(phone_window) {
            last_security_status=event->connect.status;lifecycle_changed=true;
        }
        phone_advertising_pending=true;
        break;
    case BLE_GAP_EVENT_DISCONNECT: {
        peer=phone_peer_find(event->disconnect.conn.conn_handle);
        if(!peer)break;
        if(phone_window){last_disconnect_reason=event->disconnect.reason;trace_pairing(7,event->disconnect.reason);}
        if(pairing_connection==peer->handle) {
            pairing_code=0;pairing_io_pending=false;pairing_connection=BLE_HS_CONN_HANDLE_NONE;
        }
        gopine_ancs_reset_peer(peer->handle);
        bool selected=connection==peer->handle;
        if(phone_window)memset(phone_data(peer),0,sizeof(*phone_data(peer)));
        memset(peer,0,sizeof(*peer));peer->handle=BLE_HS_CONN_HANDLE_NONE;
        if(selected) {
            connection=BLE_HS_CONN_HANDLE_NONE;
            for(unsigned i=0;i<2;i++)if(phone_peers[i].handle!=BLE_HS_CONN_HANDLE_NONE) {
                connection=phone_peers[i].handle;break;
            }
            music_snapshot_dirty=true;
        }
        lifecycle_changed=true;phone_advertising_pending=true;
        if(phone_window)slow_advertising=(int32_t)(ble_npl_time_get()-fast_until)>=0;
        break;
    }
    case BLE_GAP_EVENT_ADV_COMPLETE:
        if(phone_window && window){slow_advertising=true;phone_advertising_pending=true;}
        break;
    case BLE_GAP_EVENT_TERM_FAILURE:
        peer=phone_peer_find(event->term_failure.conn_handle);
        if(peer && peer->close_requested) {
            peer->terminating=false;peer->close_retry_at=ble_npl_time_get()+100;
        }
        break;
    case BLE_GAP_EVENT_SUBSCRIBE:
        peer=phone_peer_find(event->subscribe.conn_handle);
        if(!peer)break;
        if(event->subscribe.attr_handle==battery_handle) {
            peer->battery_subscribed=event->subscribe.cur_notify;
            if(!peer->battery_subscribed)peer->battery_snapshot_pending=false;
        }
        if(phone_window && event->subscribe.attr_handle==music_handle) {
            peer->music_subscribed=event->subscribe.cur_notify;
            phone_data(peer)->music.dirty=true;
            if(connection==peer->handle)music_snapshot_dirty=true;
        }
        break;
    case BLE_GAP_EVENT_NOTIFY_RX:
        if(window && phone_window && !expired() && phone_peer_find(event->notify_rx.conn_handle) &&
           phone_link_ready(event->notify_rx.conn_handle))
            gopine_ancs_notify(event->notify_rx.conn_handle,event->notify_rx.attr_handle,event->notify_rx.om);
        break;
    }
    return 0;
}

static void advertise(void) {
    if (!window || pending || !synced) return;
    if(update_window) {
        if(!update_links_available(&update_links))return;
    } else if(phone_window) {
        if(gopine_ble_peer_count()==2)return;
    } else if(connection!=BLE_HS_CONN_HANDLE_NONE)return;
    if(ble_gap_adv_active())return;
    if(expired()) return;
    int32_t remaining = unbounded ? BLE_HS_FOREVER : (int32_t)(expires_at - ble_npl_time_get());
    if(phone_window && !slow_advertising) {
        int32_t fast_left=(int32_t)(fast_until-ble_npl_time_get());
        if(fast_left<=0) slow_advertising=true;
        else if(unbounded || remaining>fast_left) remaining=fast_left;
    }
    struct ble_hs_adv_fields fields = {0};
    static const ble_uuid16_t uuids[] = {BLE_UUID16_INIT(0x1805), BLE_UUID16_INIT(0x180f)};
    fields.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
    fields.name = (uint8_t *)"InfiniTime"; // InfiniLink discovery filter.
    fields.name_len = 10;
    fields.name_is_complete = 1;
    fields.uuids16 = uuids;
    fields.num_uuids16 = 2;
    fields.uuids16_is_complete = 1;
    struct ble_hs_adv_fields response={0};
    if(update_window) {
        fields.name=NULL;fields.name_len=0;fields.name_is_complete=0;
        fields.uuids16=NULL;fields.num_uuids16=0;fields.uuids16_is_complete=0;
        fields.uuids128=&update_uuids[0];fields.num_uuids128=1;fields.uuids128_is_complete=1;
        response.name=(uint8_t*)"goPine Update";response.name_len=13;response.name_is_complete=1;
    }
    int rc=ble_gap_adv_rsp_set_fields(&response);
    if(rc){failure=rc;return;}
    rc = ble_gap_adv_set_fields(&fields);
    if (rc) { failure = rc; return; }
    struct ble_gap_adv_params params = {0};
    params.conn_mode = BLE_GAP_CONN_MODE_UND;
    params.disc_mode = BLE_GAP_DISC_MODE_GEN;
    params.itvl_min = slow_advertising ? 1600 : (phone_window?32:160);
    params.itvl_max = slow_advertising ? 2400 : (phone_window?48:240);
    failure = ble_gap_adv_start(address_type, NULL, remaining, &params, gap_event, NULL);
    if(failure && phone_window){last_security_status=failure;trace_pairing(10,failure);lifecycle_changed=true;}
}

static void update_links_poll(void) {
    if(!window || !update_window || expired())return;
    uint32_t now=ble_npl_time_get();
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++) {
        struct update_link *link=&update_links.entries[i];
        if(link->handle==BLE_HS_CONN_HANDLE_NONE || link->terminating)continue;
        if(!link->close_requested && link->handle==update_links.owner)update_tune(link);
        // The maintained sender reconnects during verification by subscribing
        // and waiting (up to 45s), without issuing another VERIFY command.
        // Allow that wait once per connection without granting write ownership.
        if(!link->close_requested && link->handle!=update_links.owner && link->subscribed &&
           update_status[1]==3 && !link->verification_wait) {
            link->verification_wait=true;link->deadline=now+UPDATE_VERIFY_GRACE_MS;
        }
        if((!link->close_requested && link->handle==update_links.owner) ||
           (int32_t)(now-link->deadline)<0)continue;
        // Retain this slot until GAP confirms disconnect. Controller command
        // acceptance alone does not make a second connection slot available.
        link->terminating=true;
        int rc=ble_gap_terminate(link->handle,BLE_ERR_REM_USER_CONN_TERM);
        if(rc && rc!=BLE_HS_ENOTCONN) {
            link->terminating=false;link->close_requested=true;
            link->deadline=now+100;
        }
    }
    if(update_links.advertising_pending) {
        update_links.advertising_pending=false;
        if(update_links_available(&update_links))advertise();
        else if(ble_gap_adv_active()) {
            int rc=ble_gap_adv_stop();
            if(rc && rc!=BLE_HS_EALREADY)failure=rc;
        }
    }
}

static void on_sync(void) {
    // Use a separate stable identity from InfiniTime recovery, avoiding stale
    // phone GATT caches when switching firmware. The manufacturer identity
    // remains recognizable, but the low address byte differs from recovery.
    uint8_t address[6];
    uint32_t lo = NRF_FICR->DEVICEADDR[0], hi = NRF_FICR->DEVICEADDR[1];
    memcpy(address, &lo, 4); memcpy(address + 4, &hi, 2);
    address[0] ^= 1; address[5] |= 0xc0;
    failure = ble_hs_id_set_rnd(address);
    address_type = BLE_OWN_ADDR_RANDOM;
    if(!failure)failure=gopine_ble_restore_privacy();
    synced = failure == 0;
    if(failure && phone_window) {
        last_security_status=failure;trace_pairing(18,(uint16_t)failure);lifecycle_changed=true;
    }
    if(synced)advertise();
}
static void on_reset(int reason) {
    synced=false;failure=reason?reason:-1;
    if(phone_window){last_security_status=(uint16_t)failure;trace_pairing(9,(uint16_t)failure);lifecycle_changed=true;}
}

static int start_window(const uint8_t *value, uint8_t battery, uint32_t window_ms, bool weather, bool phone) {
    // Signed deadline arithmetic requires a positive window below 2^31 ms.
    if ((!window_ms && !phone) || window_ms > INT32_MAX) return BLE_HS_EINVAL;
    if (init_failure) return init_failure;
    if (stopping || host_up) return BLE_HS_EBUSY;
    if (!initialized) {
        nimble_port_init();
        gopine_ble_set_host_queue(nimble_port_get_dflt_eventq());
        gopine_bond_init();
        ble_svc_gap_init();
        ble_svc_gatt_init();
        int rc = ble_gatts_count_cfg(services);
        if (rc) { ble_phy_disable(); ble_hw_rng_stop(); ble_phy_rfclk_disable(); init_failure = rc; return rc; }
        rc = ble_gatts_add_svcs(services);
        if (rc) { ble_phy_disable(); ble_hw_rng_stop(); ble_phy_rfclk_disable(); init_failure = rc; return rc; }
        ble_svc_gap_device_name_set("InfiniTime");
        ble_svc_gap_device_appearance_set(0x00c2); // Sports watch.
        ble_hs_cfg.sync_cb = on_sync;
        ble_hs_cfg.reset_cb = on_reset;
        // These are the initialization steps of NimBLE's ble_ll_task;
        // event queues are subsequently drained by our cooperative pump.
        ble_phy_init();
        ble_phy_txpwr_set(0);
        ble_hci_trans_cfg_ll(ble_ll_hci_cmd_rx, NULL, ble_ll_hci_acl_rx, NULL);
        ble_ll_hci_send_noop();
        initialized = true;
    }
    if(!gopine_bond_prepare(battery)) {
        ble_phy_disable();ble_hw_rng_stop();ble_phy_rfclk_disable();
        return BLE_HS_ESTORE_CAP;
    }
    // The controller RF manager owns HFXO start/stop around scheduled events.
    memcpy(current, value, 10);
    battery_level = battery > 100 ? 100 : battery;
    pending = false; window = true; failure = 0; disconnect_after = 0;
    weather_window = weather; phone_window=phone;
    unbounded=phone && !window_ms; slow_advertising=false;
    phone_peers_reset();
    update_links_reset(&update_links);
    memset(&mode_payload,0,sizeof(mode_payload));
    expires_at = ble_npl_time_get() + window_ms;
    fast_until = ble_npl_time_get() + 30000;
    if(phone){trace_pairing(1,0);trace_pairing(14,trace_bond_flags(BLE_ADDR_ANY));}
    if (!host_up) { host_up = true; ble_hs_sched_start(); }
    else advertise();
    return failure;
}

int gopine_ble_start(const uint8_t *value, uint8_t battery, uint32_t window_ms) {
    return start_window(value, battery, window_ms, false, false);
}
int gopine_ble_start_weather(const uint8_t *value, uint8_t battery, uint32_t window_ms) {
    return start_window(value, battery, window_ms, true, false);
}
int gopine_ble_take_weather(uint8_t *value) {
    if (!window || !(weather_window || phone_window)) return 0;
    if(phone_window) {
        for(unsigned i=0;i<2;i++) {
            unsigned at=(weather_cursor+i)%2;
            struct phone_peer *peer=&phone_peers[at];
            if(peer->handle==BLE_HS_CONN_HANDLE_NONE)continue;
            if(phone_link_permission(peer->handle)) {
                memset(&phone_data(peer)->weather,0,sizeof(phone_data(peer)->weather));continue;
            }
            if(!phone_link_ready(peer->handle))continue;
            int size=weather_take(&phone_data(peer)->weather,value);
            if(size){weather_cursor=(at+1)%2;return size;}
        }
        return 0;
    }
    return weather_take(&weather_incoming, value);
}

void gopine_ble_stop(void) {
    if(phone_window && window)trace_pairing(12,0);
    pairing_code=0;pairing_io_pending=false;pairing_connection=BLE_HS_CONN_HANDLE_NONE;
    gopine_ancs_reset();
    window = false; pending = false; weather_window = phone_window = false;
    phone_peers_reset();
    update_links_reset(&update_links);
    update_window=false;
    memset(&mode_payload,0,sizeof(mode_payload));
    if (!initialized || !host_up || stopping) return;
    // Allow the ATT Write Response to leave the controller before teardown.
    // The window is already closed: no new write or reconnection is accepted.
    if (disconnect_after && (int32_t)(ble_npl_time_get() - disconnect_after) < 0) return;
    disconnect_after = 0;
    stopping = true;
    int rc = ble_hs_stop(&stop_listener, stopped, NULL);
    if (rc == BLE_HS_EALREADY) {
        // Start may still be queued. Let it finish, then stop from poll.
        stopping = false;
    } else if (rc) {
        failure = rc;
        stopped(rc, NULL);
    }
}

void gopine_ble_poll(void) {
    if (!initialized || (!host_up && !stopping)) return;
    gopine_ble_pump();
    uint32_t now=ble_npl_time_get();
    for(unsigned i=0;i<2;i++) {
        struct phone_peer *peer=&phone_peers[i];
        if(peer->handle==BLE_HS_CONN_HANDLE_NONE)continue;
        if(peer->close_requested) {
            if(peer->terminating || (int32_t)(now-peer->close_retry_at)<0)continue;
            peer->terminating=true;
            int rc=ble_gap_terminate(peer->handle,BLE_ERR_REM_USER_CONN_TERM);
            if(rc && rc!=BLE_HS_ENOTCONN) {
                peer->terminating=false;peer->close_retry_at=now+100;
            }
            continue;
        }
        if(window && phone_window && peer->security_pending &&
           (int32_t)(now-peer->security_retry_at)>=0) {
            int rc=ble_gap_security_initiate(peer->handle);
            // SMP resources remain bounded even with overlapping saved-key
            // restores. Retry resource pressure without dropping either peer.
            if(rc==BLE_HS_ENOMEM || rc==BLE_HS_EBUSY) {
                peer->security_retry_at=now+1000;
            } else {
                peer->security_pending=false;trace_pairing(3,rc);
                if(rc && rc!=BLE_HS_EALREADY) {
                    last_security_status=rc;lifecycle_changed=true;
                    phone_peer_close(peer);
                }
            }
        }
    }
    if(pairing_io_pending) {
        pairing_io_pending=false;
        struct phone_peer *peer=phone_peer_find(pairing_connection);
        if(window && !update_window && peer && !peer->close_requested &&
           gopine_bond_can_pair(peer->handle)) {
            uint32_t random;
            // Use hardware entropy, never the scheduling-jitter LCG.
            do {ble_ll_rand_data_get((uint8_t*)&random,sizeof(random));} while(random>=4294000000u);
            struct ble_sm_io io={.action=BLE_SM_IOACT_DISP,.passkey=random%1000000u};
            pairing_code=io.passkey+1;lifecycle_changed=true;
            int rc=ble_sm_inject_io(peer->handle,&io);
            trace_pairing(5,rc);
            if(rc) {
                last_security_status=rc;lifecycle_changed=true;pairing_code=0;
                phone_peer_close(peer);
            }
        }
    }
    if(window && phone_window && !expired()) {
        uint16_t progress=pairing_connection==BLE_HS_CONN_HANDLE_NONE?0:
            gopine_sm_progress(pairing_connection);
        for(unsigned i=0;i<2;i++) {
            struct phone_peer *peer=&phone_peers[i];
            if(peer->handle==BLE_HS_CONN_HANDLE_NONE)continue;
            if(!progress)progress=gopine_sm_progress(peer->handle);
            gopine_ancs_poll(peer->handle,phone_link_ready(peer->handle));
        }
        // Publish one selected procedure per poll. Alternating two phases
        // would keep waking Go and fill the trace even when neither advanced.
        if(progress && progress!=pairing_progress) {
            pairing_progress=progress;trace_pairing(8,progress);lifecycle_changed=true;
        }
    }
    if(phone_advertising_pending) {
        phone_advertising_pending=false;
        advertise();
    }
    update_links_poll();
    update_notify();
    battery_snapshot_notify();
    if (window && expired()) gopine_ble_stop();
    if (!window && host_up && !stopping) gopine_ble_stop();
    finish_stop();
    int before=gopine_bond_status();
    gopine_bond_poll(!host_up);
    if(before!=gopine_bond_status())lifecycle_changed=true;
}
void gopine_ble_controller_poll(void) {
    if(initialized && (host_up || stopping)) gopine_ble_controller_pump();
}

uint32_t gopine_ble_pairing_code(void) {return pairing_code;}
int gopine_ble_cancel_pairing(void) {
    struct phone_peer *peer=phone_peer_find(pairing_connection);
    if(peer)phone_peer_close(peer);
    pairing_code=0;pairing_io_pending=false;pairing_connection=BLE_HS_CONN_HANDLE_NONE;
    lifecycle_changed=true;
    return 0;
}
uint32_t gopine_ble_pairing_diagnostics(void) {
    return last_security_status | ((uint32_t)last_disconnect_reason<<16);
}
uint16_t gopine_ble_pairing_progress(void) {return pairing_progress;}
int gopine_ble_bond_status(void) {return initialized?gopine_bond_status():-2;}
int gopine_ble_forget_phone(void) {
    if(host_up || stopping)return BLE_HS_EBUSY;
    if(!gopine_bond_forget())return BLE_HS_ESTORE_FAIL;
    last_security_status=last_disconnect_reason=0;
    pairing_progress=0;
    return 0;
}

int gopine_ble_busy(void) { return initialized && (host_up || stopping); }

int gopine_ble_take(uint8_t *value, uint32_t *age) {
    if(window && phone_window && !expired()) {
        for(unsigned i=0;i<2;i++) {
            unsigned at=(time_cursor+i)%2;
            struct phone_peer *peer=&phone_peers[at];
            if(peer->handle==BLE_HS_CONN_HANDLE_NONE)continue;
            int size=phone_time_take(&phone_data(peer)->time,peer->handle,peer->generation,
                                     ble_npl_time_get(),value,age);
            if(size){time_cursor=(at+1)%2;return size;}
        }
        return 0;
    }
    if (!pending || !window) return 0;
    memcpy(value, incoming, incoming_len);
    *age = ble_npl_time_get() - received_at;
    // Keep the mailbox locked until Stop/Start. A second sender cannot replace
    // the proposal while the user is reading the confirmation screen.
    window = false;
    disconnect_after = ble_npl_time_get() + 200;
    return incoming_len;
}
int gopine_ble_error(void) { return failure; }

void gopine_ble_phone_setup(void) {
    if(!window || !phone_window || stopping)return;
    fast_until=ble_npl_time_get()+30000;
    slow_advertising=false;
    if(gopine_ble_peer_count()==2)return;
    int rc=ble_gap_adv_stop();
    if(rc && rc!=BLE_HS_EALREADY){failure=rc;last_security_status=rc;trace_pairing(10,rc);lifecycle_changed=true;return;}
    advertise();
}
int gopine_ble_start_phone(const uint8_t *value, uint8_t battery, uint32_t window_ms) {
    return start_window(value,battery,window_ms,false,true);
}
int gopine_ble_start_update(uint8_t battery) {
    // Do not alter an active phone's mode or overlaid feature payloads when
    // the caller has not yet completed the previous asynchronous shutdown.
    if(stopping || host_up)return BLE_HS_EBUSY;
    // Select the OTA identity/advertisements before the scheduled host start.
    update_window=true;
    memset(update_status,0,sizeof(update_status));update_status[0]=2;update_status[1]=1;
    uint8_t time[10]={0};
    int rc=start_window(time,battery,600000,false,false);
    if(rc)update_window=false;
    return rc;
}
void gopine_ble_update_status(const uint8_t *status) {
    if(!window || !update_window)return;
    update_complete(&update_incoming,status);
    // Byte 3 belongs to the transport, not the Go state machine.
    if(memcmp(update_status,status,3) || memcmp(update_status+4,status+4,12)) {
        memcpy(update_status,status,3);memcpy(update_status+4,status+4,12);
        for(unsigned i=0;i<UPDATE_LINK_COUNT;i++) {
            struct update_link *link=&update_links.entries[i];
            if(link->subscribed && (update_links.owner==BLE_HS_CONN_HANDLE_NONE ||
               update_links.owner==link->handle))link->dirty=true;
        }
    }
    if(window && update_window)expires_at=ble_npl_time_get()+600000;
}
int gopine_ble_take_update(uint8_t *out,unsigned *size) {
    if(!window || !update_window){*size=0;return 0;}
    return update_take(&update_incoming,out,size);
}
int gopine_ble_take_notification(uint8_t *out) {
    if(!window || !phone_window || expired())return 0;
    for(unsigned i=0;i<2;i++) {
        unsigned at=(notification_cursor+i)%2;
        struct phone_peer *peer=&phone_peers[at];
        if(peer->handle==BLE_HS_CONN_HANDLE_NONE)continue;
        if(phone_link_permission(peer->handle)) {
            memset(&phone_data(peer)->notification,0,sizeof(phone_data(peer)->notification));continue;
        }
        if(!phone_link_ready(peer->handle))continue;
        int size=notification_take(&phone_data(peer)->notification,out);
        if(size){notification_cursor=(at+1)%2;return size;}
    }
    return 0;
}
int gopine_ble_take_apple_notification(uint8_t *out) {
    return gopine_ancs_take(out,window && phone_window && !expired());
}
int gopine_ble_take_music(uint8_t *out) {
    if(!phone_window || !music_snapshot_dirty)return 0;
    struct phone_peer *peer=phone_peer_find(connection);
    enum phone_link_state link=phone_link_snapshot(connection,peer && peer->music_subscribed);
    bool trusted=link==PHONE_LINK_CONNECTED || link==PHONE_LINK_MUSIC_READY;
    if(peer && !trusted && phone_link_permission(peer->handle))music_clear(&phone_data(peer)->music);
    // Metadata stays with the peer that supplied it. The source generation
    // routes subsequent controls even if another peer connects in the interim.
    memset(out,0,87);
    if(peer && trusted) {
        memcpy(out,phone_data(peer)->music.track,40);memcpy(out+40,phone_data(peer)->music.artist,40);
        out[80]=phone_data(peer)->music.status;out[82]=phone_data(peer)->music.status_known;
    }
    out[81]=link;
    uint32_t generation=peer?peer->generation:peer_generation;
    for(unsigned i=0;i<4;i++)out[83+i]=(uint8_t)(generation>>(i*8));
    if(peer)phone_data(peer)->music.dirty=false;
    music_snapshot_dirty=false;
    return 1;
}
int gopine_ble_music_command(uint8_t command,uint32_t generation) {
    if(!music_command_valid(command))return BLE_HS_EINVAL;
    if(!window || !phone_window || expired())return BLE_HS_ENOTCONN;
    struct phone_peer *peer=NULL;
    for(unsigned i=0;i<2;i++)if(phone_peers[i].handle!=BLE_HS_CONN_HANDLE_NONE &&
        phone_peers[i].generation==generation){peer=&phone_peers[i];break;}
    if(!peer || peer->close_requested || !peer->music_subscribed || !phone_link_ready(peer->handle))return BLE_HS_ENOTCONN;
    struct os_mbuf *om=ble_hs_mbuf_from_flat(&command,1);
    if(!om)return BLE_HS_ENOMEM;
    return ble_gattc_notify_custom(peer->handle,music_handle,om);
}
static uint32_t phone_peers_delay(uint32_t now) {
    if(phone_advertising_pending)return 0;
    uint32_t delay=UINT32_MAX;
    for(unsigned i=0;i<2;i++) {
        struct phone_peer *peer=&phone_peers[i];
        if(peer->handle==BLE_HS_CONN_HANDLE_NONE)continue;
        if(peer->close_requested) {
            if(!peer->terminating) {
                int32_t left=(int32_t)(peer->close_retry_at-now);
                uint32_t due=left>0?(uint32_t)left:0;
                if(due<delay)delay=due;
            }
            continue;
        }
        if(peer->battery_snapshot_pending)return 0;
        if(peer->security_pending) {
            int32_t left=(int32_t)(peer->security_retry_at-now);
            uint32_t due=left>0?(uint32_t)left:0;
            if(due<delay)delay=due;
        }
    }
    return delay;
}
int gopine_ble_updates(void) {
    // Input waits only pump controller work. Timed per-peer work must return
    // to the host poll as well as limiting its idle delay.
    if(lifecycle_changed || (gopine_ble_busy() &&
       (gopine_ble_host_work_pending() || gopine_bond_delay()==0)) || (!update_window && gopine_ancs_updates()))return 1;
    if(!window)return 0;
    if(failure || pending || (update_window && update_pending(&update_incoming)))return 1;
    if(update_window && update_links_delay(&update_links,ble_npl_time_get())==0)return 1;
    if(!phone_window)return weather_window && (weather_incoming.sizes[0] || weather_incoming.sizes[1]);
    if(music_snapshot_dirty || phone_peers_delay(ble_npl_time_get())==0)return 1;
    for(unsigned i=0;i<2;i++) {
        struct phone_peer *peer=&phone_peers[i];
        if(phone_link_ready(peer->handle) && (phone_data(peer)->weather.sizes[0] || phone_data(peer)->weather.sizes[1] ||
           phone_data(peer)->time.size || phone_data(peer)->notification.count))return 1;
    }
    return 0;
}
void gopine_ble_ack_updates(void) { lifecycle_changed=false; }
uint32_t gopine_ble_idle_ms(void) {
    if(!gopine_ble_busy())return 240000;
    uint32_t delay=gopine_ble_next_work(),now=ble_npl_time_get();
    if(window && update_window) {
        uint32_t update_delay=update_links_delay(&update_links,now);
        if(update_delay<delay)delay=update_delay;
    }
    if(window && phone_window) {
        uint32_t peer_delay=phone_peers_delay(now),ancs_delay=gopine_ancs_delay();
        if(peer_delay<delay)delay=peer_delay;
        if(ancs_delay<delay)delay=ancs_delay;
    }
    uint32_t bond_delay=gopine_bond_delay();
    if(bond_delay<delay)delay=bond_delay;
    if(window && !unbounded) {
        int32_t left=(int32_t)(expires_at-now);
        uint32_t due=left>0?(uint32_t)left:0;
        if(due<delay)delay=due;
    }
    if(!window && !stopping) {
        int32_t left=(int32_t)(disconnect_after-now);
        uint32_t due=disconnect_after && left>0?(uint32_t)left:0;
        if(due<delay)delay=due;
    }
    return delay;
}
