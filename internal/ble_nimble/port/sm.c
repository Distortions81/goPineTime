// The pinned host updates the connection's bonded state after fresh key
// exchange, but omits that bit from the result passed to GAP. Also, the newly
// learned peer identity is installed only later, while persisting keys. Save
// pre-pairing subscriptions after that identity and both keys are stored, not
// under the temporary address used for initial pairing. Keep vendor sources
// intact and preserve GAP's callback-before-GATT ordering.
#include "ble_hs_priv.h"

static void gopine_sm_gap_enc_event(uint16_t conn_handle, int status,
                                    int security_restored, int bonded);
static int gopine_sm_store_our_sec(const struct ble_store_value_sec *value);
static int gopine_sm_store_peer_sec(const struct ble_store_value_sec *value);
static int gopine_sm_read_our_sec(const struct ble_store_key_sec *key,
                                 struct ble_store_value_sec *value);
extern void gopine_ble_trace_security(uint8_t event, uint16_t status);
extern bool gopine_bond_can_pair(uint16_t conn_handle);
#define ble_gap_enc_event gopine_sm_gap_enc_event
#define ble_store_write_our_sec gopine_sm_store_our_sec
#define ble_store_write_peer_sec gopine_sm_store_peer_sec
// The pinned SM reads OUR_SEC only when answering a controller LTK request.
// A missing key otherwise removes the procedure without any GAP callback.
#define ble_store_read_our_sec gopine_sm_read_our_sec
// Function-like substitution renames the definition; the vendor's typedef
// declaration and dispatch-table entry still name our checked wrapper below.
#define ble_sm_pair_req_rx(...) gopine_sm_upstream_pair_req_rx(__VA_ARGS__)
#include "ble_sm.c"
#undef ble_gap_enc_event
#undef ble_store_write_our_sec
#undef ble_store_write_peer_sec
#undef ble_store_read_our_sec
#undef ble_sm_pair_req_rx

static int gopine_sm_read_our_sec(const struct ble_store_key_sec *key,
                                 struct ble_store_value_sec *value) {
    int rc=ble_store_read_our_sec(key,value);
    // Status only: never copy the lookup identity or returned key material.
    gopine_ble_trace_security(16,(uint16_t)rc);
    return rc;
}

static bool gopine_sm_other_pairing(uint16_t conn_handle) {
    bool pairing=false;
    ble_hs_lock();
    struct ble_sm_proc *proc;
    STAILQ_FOREACH(proc,&ble_sm_procs,next) {
        if(proc->conn_handle==conn_handle)continue;
        // A Security Request or stored-key restore does not own a fresh PIN.
        if(proc->state!=BLE_SM_PROC_STATE_SEC_REQ &&
           proc->state!=BLE_SM_PROC_STATE_LTK_RESTORE &&
           proc->state!=BLE_SM_PROC_STATE_ENC_RESTORE &&
           proc->state!=BLE_SM_PROC_STATE_NONE) {pairing=true;break;}
    }
    ble_hs_unlock();
    return pairing;
}
static void ble_sm_pair_req_rx(uint16_t conn_handle, struct os_mbuf **om,
                               struct ble_sm_result *res) {
    // Preserve the pinned host's packet, role, in-progress and saved-bond
    // checks. Its accepted request has not yet executed a Pairing Response.
    gopine_sm_upstream_pair_req_rx(conn_handle,om,res);
    if(res->app_status) {
        // Upstream rejection paths can omit enc_cb and disappear before the
        // application polls progress. Preserve their original wire behavior.
        gopine_ble_trace_security(17,(uint16_t)res->app_status);
        return;
    }
    if(!res->execute) return;
    uint8_t reason=gopine_bond_can_pair(conn_handle) && !gopine_sm_other_pairing(conn_handle)?
        0:BLE_SM_ERR_AUTHREQ;
    uint8_t action=BLE_SM_IOACT_NONE;
    ble_hs_lock();
    struct ble_sm_proc *proc=ble_sm_proc_find(conn_handle,BLE_SM_PROC_STATE_PAIR,-1,NULL);
    if(proc) {
        if(proc->key_size!=16) reason=BLE_SM_ERR_ENC_KEY_SZ;
        else if(!(proc->flags & BLE_SM_PROC_F_BONDING) ||
                ble_sm_io_action(proc,&action) || action!=BLE_SM_IOACT_DISP)
            reason=BLE_SM_ERR_AUTHREQ;
    }
    ble_hs_unlock();
    if(reason) {
        // Normal result handling sends Pairing Failed, frees the new procedure
        // and reports the error to GAP. No public key or bond is generated.
        res->execute=0;res->enc_cb=1;res->sm_err=reason;
        res->app_status=BLE_HS_SM_US_ERR(reason);
    }
}

// Fresh pairing is serialized even though stored-key restores can overlap.
// The pinned result handler calls these hooks synchronously in ENC_CHANGE ->
// our key -> peer key order; each stage verifies the same peer identity.
static uint16_t pending_bond = BLE_HS_CONN_HANDLE_NONE;
static bool pending_our_key;
static ble_addr_t pending_identity;

// Read only the protocol phase and verified passkey-round count. Never export
// keys, nonces or the private scalar. Called after the host pump has unwound.
// The high byte is our stable UI phase, not NimBLE's private state numbering.
uint16_t gopine_sm_progress(uint16_t conn_handle) {
    uint16_t progress = 0;
    ble_hs_lock();
    struct ble_sm_proc *proc = ble_sm_proc_find(conn_handle,
                                               BLE_SM_PROC_STATE_NONE, -1, NULL);
    if (proc) {
        unsigned phase = 1;
        switch (proc->state) {
        case BLE_SM_PROC_STATE_CONFIRM:
        case BLE_SM_PROC_STATE_RANDOM: phase = 2; break;
        case BLE_SM_PROC_STATE_DHKEY_CHECK: phase = 3; break;
        case BLE_SM_PROC_STATE_LTK_START:
        case BLE_SM_PROC_STATE_LTK_RESTORE:
        case BLE_SM_PROC_STATE_ENC_START:
        case BLE_SM_PROC_STATE_ENC_RESTORE: phase = 4; break;
        case BLE_SM_PROC_STATE_KEY_EXCH: phase = 5; break;
        }
        unsigned rounds = proc->passkey_bits_exchanged;
        // The pinned host increments when it generates a confirm, before
        // verifying the peer random. Count only finished rounds in the UI.
        if (proc->state == BLE_SM_PROC_STATE_RANDOM && rounds) rounds--;
        progress = (phase << 8) | rounds;
        if (proc->state == BLE_SM_PROC_STATE_RANDOM) progress |= 0x80;
    }
    ble_hs_unlock();
    return progress;
}

static bool gopine_sm_bond_connection(struct ble_gap_conn_desc *desc) {
    return pending_bond != BLE_HS_CONN_HANDLE_NONE &&
        !ble_gap_conn_find(pending_bond, desc) && desc->sec_state.encrypted &&
        desc->sec_state.authenticated && desc->sec_state.bonded &&
        desc->sec_state.key_size == 16;
}
static bool gopine_sm_bond_key(const struct ble_store_value_sec *value,
                              const struct ble_gap_conn_desc *desc) {
    return value->ltk_present && value->authenticated && value->sc &&
        value->key_size==16 && !ble_addr_cmp(&desc->peer_id_addr,&value->peer_addr);
}

static void gopine_sm_gap_enc_event(uint16_t conn_handle, int status,
                                    int security_restored, int bonded) {
    pending_bond = BLE_HS_CONN_HANDLE_NONE;
    pending_our_key = false;
    if (!status && !security_restored) {
        struct ble_gap_conn_desc desc;
        pending_bond = conn_handle;
        if (!gopine_sm_bond_connection(&desc))
            pending_bond = BLE_HS_CONN_HANDLE_NONE;
        // GATT establishment must wait for identity/key persistence. Restore
        // and failure calls retain the exact upstream arguments and behavior.
        bonded = 0;
    }
    ble_gap_enc_event(conn_handle, status, security_restored, bonded);
}

static int gopine_sm_store_our_sec(const struct ble_store_value_sec *value) {
    struct ble_gap_conn_desc desc;
    // MITM in Pairing Response does not force the pinned host to reject
    // Just Works. Never let its unusable keys occupy a saved peer slot.
    // Identity distribution can reveal that an initially unknown RPA belongs
    // to a saved peer. Recheck after that identity is installed, before either
    // key is written; fresh pairing must never replace another saved bond.
    if (!gopine_sm_bond_connection(&desc) || !gopine_sm_bond_key(value,&desc) ||
        !gopine_bond_can_pair(pending_bond)) {
        pending_bond = BLE_HS_CONN_HANDLE_NONE;
        pending_our_key = false;
        return BLE_HS_EAUTHEN;
    }
    int rc = ble_store_write_our_sec(value);
    if (!rc) {
        pending_identity = value->peer_addr;
        pending_our_key = true;
    } else {
        pending_bond = BLE_HS_CONN_HANDLE_NONE;
        pending_our_key = false;
    }
    return rc;
}

static int gopine_sm_store_peer_sec(const struct ble_store_value_sec *value) {
    struct ble_gap_conn_desc desc;
    if (!pending_our_key || !gopine_sm_bond_connection(&desc) ||
        !gopine_sm_bond_key(value,&desc) ||
        ble_addr_cmp(&pending_identity, &value->peer_addr)) {
        pending_bond = BLE_HS_CONN_HANDLE_NONE;
        pending_our_key = false;
        return BLE_HS_EAUTHEN;
    }
    int rc = ble_store_write_peer_sec(value);
    bool establish = !rc && gopine_sm_bond_connection(&desc) &&
        !ble_addr_cmp(&desc.peer_id_addr, &value->peer_addr);
    uint16_t conn_handle = pending_bond;
    pending_bond = BLE_HS_CONN_HANDLE_NONE;
    pending_our_key = false;
    if (establish) ble_gatts_bonding_established(conn_handle);
    return rc;
}
