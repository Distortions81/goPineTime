// The pinned host restores peer IRKs before its sync callback, but logs and
// discards individual HCI failures. Perform a checked replay before advertising.
// Replace any entry it already installed: the real controller rejects a second
// Add Resolving List command for the same identity instead of replacing it.
#include "ble_hs_priv.h"

static int restore_peer_irk(int type, union ble_store_value *value, void *arg) {
    if(type!=BLE_STORE_OBJ_TYPE_PEER_SEC || !value->sec.irk_present ||
       !ble_addr_cmp(&value->sec.peer_addr,BLE_ADDR_ANY))return 0;
    int rc=ble_hs_pvcy_remove_entry(value->sec.peer_addr.type,
                                   value->sec.peer_addr.val);
    // A failed upstream replay leaves no entry. Other removal failures must
    // remain visible, and neither operation changes the saved bond journal.
    if(!rc || rc==BLE_HS_HCI_ERR(BLE_ERR_UNK_CONN_ID))
        rc=ble_hs_pvcy_add_entry(value->sec.peer_addr.val,
                                value->sec.peer_addr.type,value->sec.irk);
    // Upstream treats a nonzero callback return as successful early stopping,
    // so carry the actual HCI failure separately instead of losing it.
    *(int*)arg=rc;
    return rc;
}

int gopine_ble_restore_privacy(void) {
    int failure=0;
    int rc=ble_store_iterate(BLE_STORE_OBJ_TYPE_PEER_SEC,restore_peer_irk,&failure);
    if(rc)return rc;
    if(failure)return failure;
    // Controller Reset clears address resolution. The pinned host's cached
    // local IRK survives Stop/Start, so startup may skip re-enabling it when
    // the IRK has not changed. Our stable random identity still needs peer
    // resolution for an iPhone using a new private address.
    struct ble_hci_le_set_addr_res_en_cp enable={.enable=1};
    return ble_hs_hci_cmd_tx(BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_SET_ADDR_RES_EN),
                             &enable,sizeof(enable),NULL,0);
}
