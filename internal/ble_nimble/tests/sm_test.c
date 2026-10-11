#include <assert.h>
#include "../port/sm.c"
// Exercise the pinned production GAP/GATT paths too: an event-only mock would
// miss subscription loss and the ordering required by restored connections.
#include "ble_gap.c"
#include "ble_gatts.c"

static struct ble_hs_conn test_conn;
static bool connected;
static struct ble_gatts_clt_cfg configs[2];
static struct ble_store_value_cccd saved[2];
static unsigned saved_count, writes, enc_events, restore_events, notifications;
static unsigned key_writes, freed_procs, identity_events;
static int expected_status, locked;
static int fail_our_key, fail_peer_key;
static bool drop_on_peer_key, change_on_peer_key;
static bool allow_new_peer;
struct ble_hs_cfg ble_hs_cfg;
bool gopine_bond_can_pair(uint16_t conn_handle) {
    assert(conn_handle==7);
    return allow_new_peer;
}
void gopine_ble_trace_security(uint8_t event,uint16_t status) {
    (void)event;(void)status;
    assert(!"unexpected lookup or pairing-request path");
}

// Including the vendor state machines retains their dispatch tables under
// ASan. Unexercised transport/cryptographic entry points must never run here.
void unexpected_vendor_path(void) { assert(!"unexpected vendor path"); }
#define UNUSED_VENDOR(name) __typeof__(name) name __attribute__((alias("unexpected_vendor_path")))
UNUSED_VENDOR(ble_hs_hci_util_rand);
UNUSED_VENDOR(ble_sm_sc_io_action);
UNUSED_VENDOR(ble_hs_conn_exists);
UNUSED_VENDOR(ble_npl_time_get);
UNUSED_VENDOR(ble_npl_time_ms_to_ticks32);
UNUSED_VENDOR(ble_hs_timer_resched);
UNUSED_VENDOR(ble_sm_cmd_get);
UNUSED_VENDOR(ble_sm_tx);
UNUSED_VENDOR(os_memblock_get);
UNUSED_VENDOR(ble_hs_hci_cmd_tx);
UNUSED_VENDOR(ble_sm_sc_confirm_exec);
UNUSED_VENDOR(ble_sm_sc_random_exec);
UNUSED_VENDOR(os_mbuf_free_chain);
UNUSED_VENDOR(ble_hs_pvcy_our_irk);
UNUSED_VENDOR(ble_hs_conn_find_assert);
UNUSED_VENDOR(ble_store_util_count);
UNUSED_VENDOR(ble_store_full_event);
UNUSED_VENDOR(ble_hs_mbuf_pullup_base);
UNUSED_VENDOR(ble_sm_sc_random_rx);
UNUSED_VENDOR(ble_store_read_peer_sec);
UNUSED_VENDOR(ble_sm_sc_public_key_exec);
UNUSED_VENDOR(ble_sm_sc_dhkey_check_exec);
UNUSED_VENDOR(ble_sm_sc_public_key_rx);
UNUSED_VENDOR(ble_sm_sc_dhkey_check_rx);
#undef UNUSED_VENDOR

int ble_store_write_our_sec(const struct ble_store_value_sec *value) {
    assert(enc_events && !writes);
    key_writes++;
    return fail_our_key;
}
int ble_store_write_peer_sec(const struct ble_store_value_sec *value) {
    assert(enc_events && !writes);
    key_writes++;
    if (drop_on_peer_key) connected = false;
    if (change_on_peer_key) test_conn.bhc_peer_addr.val[0]++;
    return fail_peer_key;
}
os_error_t os_memblock_put(struct os_mempool *pool, void *block) {
    assert(pool == &ble_sm_proc_pool && block != NULL);
    freed_procs++;
    return 0;
}

void ble_hs_lock(void) { assert(!locked); locked = 1; }
void ble_hs_unlock(void) { assert(locked); locked = 0; }
int ble_hs_locked_by_cur_task(void) { return locked; }
struct ble_hs_conn *ble_hs_conn_find(uint16_t handle) {
    return connected && handle == test_conn.bhc_handle ? &test_conn : NULL;
}
void ble_hs_conn_addrs(const struct ble_hs_conn *conn, struct ble_hs_conn_addrs *addrs) {
    memset(addrs, 0, sizeof(*addrs));
    addrs->peer_id_addr = conn->bhc_peer_addr;
    addrs->peer_id_addr.type = ble_hs_misc_peer_addr_type_to_id(addrs->peer_id_addr.type);
}
uint8_t ble_hs_misc_peer_addr_type_to_id(uint8_t type) {
    return type >= BLE_ADDR_PUBLIC_ID ? type - BLE_ADDR_PUBLIC_ID : type;
}
int ble_store_write_cccd(const struct ble_store_value_cccd *value) {
    assert(!locked);
    // GAP must notify the application before persisting fresh subscriptions.
    assert(enc_events && key_writes == 2);
    unsigned i;
    for (i = 0; i < saved_count; i++) {
        if (saved[i].chr_val_handle == value->chr_val_handle &&
            !ble_addr_cmp(&saved[i].peer_addr, &value->peer_addr)) break;
    }
    assert(i < 2);
    if (i == saved_count) saved_count++;
    saved[i] = *value;
    writes++;
    return 0;
}
int ble_store_read_cccd(const struct ble_store_key_cccd *key,
                        struct ble_store_value_cccd *value) {
    assert(!locked);
    unsigned matching = 0;
    for (unsigned i = 0; i < saved_count; i++) {
        if (ble_addr_cmp(&saved[i].peer_addr, &key->peer_addr)) continue;
        if (matching++ != key->idx) continue;
        *value = saved[i];
        return 0;
    }
    return BLE_HS_ENOENT;
}
int ble_gattc_notify(uint16_t handle, uint16_t attr) {
    assert(handle == test_conn.bhc_handle && (attr == 10 || attr == 20));
    notifications++;
    return 0;
}
int ble_gattc_indicate(uint16_t handle, uint16_t attr) {
    (void)handle; (void)attr;
    assert(false);
    return 0;
}
static int event_cb(struct ble_gap_event *event, void *arg) {
    (void)arg;
    assert(!locked);
    if (event->type == BLE_GAP_EVENT_ENC_CHANGE) {
        assert(event->enc_change.status == expected_status);
        enc_events++;
    } else if (event->type == BLE_GAP_EVENT_IDENTITY_RESOLVED) {
        assert(enc_events == 1 && !writes && !key_writes);
        identity_events++;
    } else {
        assert(event->type == BLE_GAP_EVENT_SUBSCRIBE);
        assert(event->subscribe.reason == BLE_GAP_SUBSCRIBE_REASON_RESTORE);
        assert(event->subscribe.cur_notify);
        restore_events++;
    }
    return 0;
}
static void reset(void) {
    memset(&test_conn, 0, sizeof(test_conn));
    memset(configs, 0, sizeof(configs));
    memset(saved, 0, sizeof(saved));
    test_conn.bhc_handle = 7;
    test_conn.bhc_peer_addr.type = BLE_ADDR_RANDOM;
    test_conn.bhc_peer_addr.val[0] = 0x11;
    test_conn.bhc_cb = event_cb;
    test_conn.bhc_gatt_svr.clt_cfgs = configs;
    test_conn.bhc_gatt_svr.num_clt_cfgs = 2;
    configs[0].chr_val_handle = 10; // Battery.
    configs[1].chr_val_handle = 20; // Music control.
    configs[0].allowed = configs[1].allowed = BLE_GATTS_CLT_CFG_F_NOTIFY;
    ble_gatts_clt_cfgs = configs;
    ble_gatts_num_cfgable_chrs = 2;
    saved_count = writes = enc_events = restore_events = notifications = 0;
    key_writes = freed_procs = identity_events = 0;
    fail_our_key = fail_peer_key = 0;
    drop_on_peer_key = change_on_peer_key = false;
    allow_new_peer = true;
    STAILQ_INIT(&ble_sm_procs);
    expected_status = locked = 0;
    connected = true;
}
static void complete_pairing(bool use_rpa, unsigned flags, uint8_t key_size) {
    // The central has subscribed before the first pairing completes. No CCCD
    // exists in storage yet. Use the real upstream key-exchange completion.
    configs[0].flags = configs[1].flags = BLE_GATTS_CLT_CFG_F_NOTIFY;
    struct ble_sm_proc proc = {.conn_handle = 7, .key_size = key_size, .flags = flags};
    proc.our_keys.ltk_valid = proc.peer_keys.ltk_valid = 1;
    proc.our_keys.ediv_rand_valid = proc.peer_keys.ediv_rand_valid = 1;
    proc.our_keys.key_size = proc.peer_keys.key_size = key_size;
    memset(proc.our_keys.ltk,0x56,sizeof(proc.our_keys.ltk));
    memcpy(proc.peer_keys.ltk,proc.our_keys.ltk,sizeof(proc.peer_keys.ltk));
    if (use_rpa) {
        test_conn.bhc_peer_rpa_addr = test_conn.bhc_peer_addr;
        proc.peer_keys.addr_valid = 1;
        proc.peer_keys.addr_type = BLE_ADDR_PUBLIC;
        proc.peer_keys.addr[0] = 0x77;
    }
    struct ble_sm_result res = {0};
    ble_sm_key_exch_success(&proc, &res);
    assert(test_conn.bhc_sec_state.encrypted && test_conn.bhc_sec_state.bonded);
    assert(test_conn.bhc_sec_state.authenticated == !!(flags & BLE_SM_PROC_F_AUTHENTICATED));
    assert(proc.state == BLE_SM_PROC_STATE_NONE);
    assert(res.enc_cb && !res.app_status);
    // The actual upstream result path invokes the wrapper, then persists keys.
    STAILQ_INSERT_HEAD(&ble_sm_procs, &proc, next);
    ble_sm_process_result(7, &res);
    unsigned expected_keys = allow_new_peer && key_size==16 && (flags & BLE_SM_PROC_F_AUTHENTICATED) &&
        (flags & BLE_SM_PROC_F_SC) ? (fail_our_key ? 1 : 2) : 0;
    assert(key_writes == expected_keys && freed_procs == 1 && STAILQ_EMPTY(&ble_sm_procs));
    assert(pending_bond == BLE_HS_CONN_HANDLE_NONE && !pending_our_key);
}
#define STRONG_PAIRING (BLE_SM_PROC_F_BONDING | BLE_SM_PROC_F_AUTHENTICATED | BLE_SM_PROC_F_SC)
static void test_pair_and_restore(bool use_rpa) {
    reset();
    complete_pairing(use_rpa,STRONG_PAIRING,16);
    assert(enc_events == 1 && writes == 2 && saved_count == 2);
    assert(saved[0].flags == BLE_GATTS_CLT_CFG_F_NOTIFY);
    assert(saved[1].flags == BLE_GATTS_CLT_CFG_F_NOTIFY);
    assert(saved[0].peer_addr.val[0] == (use_rpa ? 0x77 : 0x11));
    assert(!ble_addr_cmp(&saved[0].peer_addr, &saved[1].peer_addr));
    assert(identity_events == (use_rpa ? 1u : 0u));

    // A reconnect restores the saved subscriptions and pending notification.
    // It must not first persist zero/default in-memory CCCDs over the journal.
    configs[0].flags = configs[1].flags = 0;
    saved[0].value_changed = 1;
    gopine_sm_gap_enc_event(7, 0, 1, 0);
    assert(enc_events == 2 && restore_events == 2 && notifications == 1);
    assert(configs[0].flags & BLE_GATTS_CLT_CFG_F_NOTIFY);
    assert(configs[1].flags & BLE_GATTS_CLT_CFG_F_NOTIFY);
    assert(writes == 3 && !saved[0].value_changed);

    // Failed encryption never establishes or restores subscriptions.
    unsigned before = writes;
    expected_status = BLE_HS_EAUTHEN;
    gopine_sm_gap_enc_event(7, expected_status, 0, 0);
    gopine_sm_gap_enc_event(7, expected_status, 1, 1);
    assert(enc_events == 4 && writes == before && restore_events == 2);
}
int main(void) {
    test_pair_and_restore(false);
    test_pair_and_restore(true);

    // Identity distribution may identify a saved peer only after encryption.
    // The actual result path must not replace either key or its subscriptions.
    reset();allow_new_peer=false;
    complete_pairing(true,STRONG_PAIRING,16);
    assert(identity_events==1 && !key_writes && !writes && !saved_count);

    // Either failed key write, a disappearing peer, or an identity mismatch
    // must leave no persisted subscriptions and no pending state to leak.
    for (unsigned mode = 0; mode < 4; mode++) {
        reset();
        fail_our_key = mode == 0 ? BLE_HS_ESTORE_FAIL : 0;
        fail_peer_key = mode == 1 ? BLE_HS_ESTORE_FAIL : 0;
        drop_on_peer_key = mode == 2;
        change_on_peer_key = mode == 3;
        complete_pairing(true,STRONG_PAIRING,16);
        assert(enc_events == 1 && !writes && !saved_count);
        connected = true;
        fail_our_key = fail_peer_key = 0;
        drop_on_peer_key = change_on_peer_key = false;
        struct ble_store_value_sec stale = {0};
        stale.peer_addr = test_conn.bhc_peer_addr;
        stale.peer_addr.type = ble_hs_misc_peer_addr_type_to_id(stale.peer_addr.type);
        assert(gopine_sm_store_peer_sec(&stale)==BLE_HS_EAUTHEN);
        assert(!writes && pending_bond == BLE_HS_CONN_HANDLE_NONE);
    }

    // Real pinned completion/result paths would otherwise persist Just Works,
    // undersized, or non-SC keys and consume a slot without usable phone
    // authentication. Reject both key writes, including with a learned identity.
    for(unsigned mode=0;mode<3;mode++) {
        reset();
        unsigned flags=STRONG_PAIRING;
        if(mode==0)flags &= ~BLE_SM_PROC_F_AUTHENTICATED;
        if(mode==2)flags &= ~BLE_SM_PROC_F_SC;
        complete_pairing(true,flags,mode==1?12:16);
        assert(enc_events==1 && !key_writes && !writes && !saved_count);
        // A later authenticated attempt must still be able to save its keys.
        test_pair_and_restore(true);
    }

    // An absent, unencrypted, or unbonded connection must not gain the flag.
    reset();
    configs[0].flags = BLE_GATTS_CLT_CFG_F_NOTIFY;
    test_conn.bhc_sec_state.bonded = 1;
    gopine_sm_gap_enc_event(7, 0, 0, 0);
    assert(!writes && pending_bond == BLE_HS_CONN_HANDLE_NONE);
    test_conn.bhc_sec_state.bonded = 0;
    test_conn.bhc_sec_state.encrypted = 1;
    gopine_sm_gap_enc_event(7, 0, 0, 0);
    assert(!writes && pending_bond == BLE_HS_CONN_HANDLE_NONE);
    test_conn.bhc_sec_state.bonded = 1;
    test_conn.bhc_sec_state.key_size = 16;
    gopine_sm_gap_enc_event(7, 0, 0, 0);
    assert(!writes && pending_bond == BLE_HS_CONN_HANDLE_NONE); // Not authenticated.
    test_conn.bhc_sec_state.authenticated = 1;
    test_conn.bhc_sec_state.key_size = 12;
    gopine_sm_gap_enc_event(7, 0, 0, 0);
    assert(!writes && pending_bond == BLE_HS_CONN_HANDLE_NONE);
    connected = false;
    gopine_sm_gap_enc_event(7, 0, 0, 0);
    assert(!writes && pending_bond == BLE_HS_CONN_HANDLE_NONE);
}
