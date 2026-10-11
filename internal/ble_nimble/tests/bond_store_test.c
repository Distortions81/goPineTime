#include <assert.h>
#include "../port/bond.c"
struct ble_hs_cfg ble_hs_cfg;
static uint32_t now;
static ble_addr_t connected_peers[3];
int ble_gap_conn_find(uint16_t handle,struct ble_gap_conn_desc *desc) {
    if(!handle || handle>3)return BLE_HS_ENOTCONN;
    memset(desc,0,sizeof(*desc));desc->peer_id_addr=connected_peers[handle-1];
    return 0;
}
uint32_t ble_npl_time_get(void){return now;}
void ble_store_key_from_value_sec(struct ble_store_key_sec *k,const struct ble_store_value_sec *v) {
    memset(k,0,sizeof(*k));k->peer_addr=v->peer_addr;
}
void ble_store_key_from_value_cccd(struct ble_store_key_cccd *k,const struct ble_store_value_cccd *v) {
    memset(k,0,sizeof(*k));k->peer_addr=v->peer_addr;k->chr_val_handle=v->chr_val_handle;
}
static uint32_t flash[BOND_PAGE_SIZE/4];
static int writes_left=-1;
uint32_t bond_flash_word(unsigned off){assert(off<sizeof(flash));return flash[off/4];}
bool bond_flash_write(unsigned off,uint32_t v){
    assert(off<sizeof(flash));
    if(writes_left==0)return false;
    if(writes_left>0)writes_left--;
    flash[off/4]&=v;return flash[off/4]==v;
}
bool bond_flash_erase(void){memset(flash,0xff,sizeof(flash));return true;}
static void read_key(int type,const union ble_store_value *expected) {
    union ble_store_key key={0};union ble_store_value out;
    key.sec.peer_addr=expected->sec.peer_addr;
    assert(!ble_hs_cfg.store_read_cb(type,&key,&out));
    assert(!memcmp(&out.sec,&expected->sec,sizeof(out.sec)));
}
static void multiple_peers(const union ble_store_value *our,const union ble_store_value *peer) {
    const uint16_t handles[]={8,27,36,20}; // Service Changed, Battery, Music, OTA.
    struct bond_state_v1 legacy={.version=1,.our_count=1,.peer_count=1,.cccd_count=4};
    legacy.our[0]=our->sec;legacy.peer[0]=peer->sec;
    for(unsigned i=0;i<4;i++) {
        legacy.cccd[i].peer_addr=our->sec.peer_addr;
        legacy.cccd[i].chr_val_handle=handles[i];
        legacy.cccd[i].flags=i?1:2;legacy.cccd[i].value_changed=!i;
    }
    union ble_store_value second_our=*our,second_peer=*peer;
    second_our.sec.peer_addr.val[0]++;
    memset(second_our.sec.ltk,0x91,sizeof(second_our.sec.ltk));
    second_peer.sec=second_our.sec;second_peer.sec.irk_present=1;
    memset(second_peer.sec.irk,0xa3,sizeof(second_peer.sec.irk));
    connected_peers[0]=our->sec.peer_addr;connected_peers[1]=second_our.sec.peer_addr;
    connected_peers[2]=second_our.sec.peer_addr;connected_peers[2].val[0]++;

    bond_flash_erase();unsigned at=0;
    assert(bond_journal_save(&legacy,sizeof(legacy),&at));
    uint32_t old_record[BOND_RECORD_SIZE/4];memcpy(old_record,flash,sizeof(old_record));
    gopine_bond_init();
    assert(gopine_bond_status()==2 && gopine_bond_count()==1);
    assert(gopine_bond_peer_saved(1) && !gopine_bond_can_pair(1));
    assert(!gopine_bond_peer_saved(2) && gopine_bond_can_pair(2));
    assert(!gopine_bond_can_pair(99));
    read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,peer);
    gopine_bond_poll(true);assert(next_record==BOND_RECORD_SIZE);
    assert(!memcmp(old_record,flash,sizeof(old_record))); // Upgrade does not erase v1.

    uint32_t pairing_time=now;
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our));
    assert(gopine_bond_status()==-3 && gopine_bond_count()==1);
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer));
    for(unsigned i=0;i<4;i++) {
        union ble_store_value cccd={0};cccd.cccd=legacy.cccd[i];
        cccd.cccd.peer_addr=second_our.sec.peer_addr;
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&cccd));
    }
    assert(gopine_bond_count()==2 && gopine_bond_status()==1);
    assert(gopine_bond_peer_saved(2) && !gopine_bond_can_pair(2) && !gopine_bond_can_pair(3));
    // The first phone continues sending identical subscription writes during
    // the second pairing. A debounce that restarts on each write never saves
    // the new peer; require it durable at the original one-second deadline.
    for(unsigned elapsed=300;elapsed<=900;elapsed+=300) {
        now=pairing_time+elapsed;
        union ble_store_value repeated={0};repeated.cccd=legacy.cccd[1];
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&repeated));
        gopine_bond_poll(false);assert(gopine_bond_status()==1);
    }
    now=pairing_time+1000;gopine_bond_poll(false);gopine_bond_init();
    assert(next_record==2*BOND_RECORD_SIZE && gopine_bond_status()==2 && gopine_bond_count()==2);
    assert(!memcmp(old_record,flash,sizeof(old_record)));
    assert(ble_store_ram_num_cccds==8);
    read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,peer);
    read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer);
    union ble_store_value third=second_our;third.sec.peer_addr=connected_peers[2];
    assert(ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&third)==BLE_HS_ESTORE_CAP);
    union ble_store_value ninth={0};ninth.cccd=legacy.cccd[0];ninth.cccd.chr_val_handle=999;
    assert(ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&ninth)==BLE_HS_ESTORE_CAP);
    assert(gopine_bond_status()==2); // Rejection never poisons valid stored keys.

    // A later torn subscription save must preserve BOTH already saved peers.
    uint32_t two_peer_flash[BOND_PAGE_SIZE/4];memcpy(two_peer_flash,flash,sizeof(flash));
    for(int cut=0;cut<BOND_RECORD_SIZE/4;cut++) {
        memcpy(flash,two_peer_flash,sizeof(flash));gopine_bond_init();
        union ble_store_value changed_cccd={0};changed_cccd.cccd=legacy.cccd[0];
        changed_cccd.cccd.value_changed=0;
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&changed_cccd));
        writes_left=cut;gopine_bond_poll(true);assert(gopine_bond_status()==-1);
        writes_left=-1;gopine_bond_init();
        assert(gopine_bond_status()==2 && gopine_bond_count()==2);
        read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,peer);
        read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer);
        assert(ble_store_ram_num_cccds==8 && ble_store_ram_cccds[0].value_changed);
    }
    memcpy(flash,two_peer_flash,sizeof(flash));gopine_bond_init();

    // Inspect all saved peers: a valid first peer cannot hide a weak second.
    union ble_store_value weak_second=second_peer;weak_second.sec.authenticated=0;
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&weak_second));
    assert(gopine_bond_status()==-3 && gopine_bond_count()==1);
    assert(gopine_bond_peer_saved(1) && !gopine_bond_peer_saved(2));
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer));

    // Delete the first peer through the real store callbacks. Every following
    // key byte and CCCD (including pending Service Changed) must survive.
    union ble_store_key key={0};key.sec.peer_addr=our->sec.peer_addr;
    assert(!ble_hs_cfg.store_delete_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&key));
    assert(!ble_hs_cfg.store_delete_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&key));
    for(unsigned i=0;i<4;i++) {
        memset(&key,0,sizeof(key));key.cccd.peer_addr=our->sec.peer_addr;
        key.cccd.chr_val_handle=handles[i];
        assert(!ble_hs_cfg.store_delete_cb(BLE_STORE_OBJ_TYPE_CCCD,&key));
    }
    gopine_bond_poll(true);gopine_bond_init();
    assert(gopine_bond_status()==2 && gopine_bond_count()==1 && gopine_bond_can_pair(1));
    read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer);
    assert(ble_store_ram_num_cccds==4);
    for(unsigned i=0;i<4;i++) {
        struct ble_store_value_cccd expected=legacy.cccd[i];expected.peer_addr=second_our.sec.peer_addr;
        assert(!memcmp(&ble_store_ram_cccds[i],&expected,sizeof(expected)));
    }

    // Power loss at each word of the first v2 append must leave the original
    // v1 peer usable. A subsequent complete append then saves both peers.
    for(int cut=0;cut<BOND_RECORD_SIZE/4;cut++) {
        bond_flash_erase();at=0;writes_left=-1;
        assert(bond_journal_save(&legacy,sizeof(legacy),&at));gopine_bond_init();
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our));
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer));
        writes_left=cut;gopine_bond_poll(true);assert(gopine_bond_status()==-1);
        writes_left=-1;gopine_bond_init();
        assert(gopine_bond_status()==2 && gopine_bond_count()==1 && gopine_bond_can_pair(2));
        read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,peer);
        assert(!memcmp(old_record,flash,sizeof(old_record)));
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our));
        assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer));
        gopine_bond_poll(true);gopine_bond_init();
        assert(gopine_bond_status()==2 && gopine_bond_count()==2);
        read_key(BLE_STORE_OBJ_TYPE_OUR_SEC,&second_our);read_key(BLE_STORE_OBJ_TYPE_PEER_SEC,&second_peer);
    }
    assert(gopine_bond_forget());
}
int main(void) {
    bond_flash_erase();gopine_bond_init();assert(gopine_bond_status()==0);
    union ble_store_value our={0},peer={0},cccd={0},out={0};
    our.sec.peer_addr.type=BLE_ADDR_PUBLIC;our.sec.peer_addr.val[0]=19;
    our.sec.ltk_present=our.sec.authenticated=our.sec.sc=1;
    our.sec.key_size=16;memset(our.sec.ltk,0x56,16);
    peer=our;peer.sec.irk_present=1;memset(peer.sec.irk,0x78,16);
    cccd.cccd.peer_addr=our.sec.peer_addr;cccd.cccd.chr_val_handle=27;cccd.cccd.flags=1;
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&our));
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&peer));
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&cccd));
    assert(gopine_bond_status()==1);now=1000;gopine_bond_poll(false);
    assert(gopine_bond_status()==2);
    unsigned written=next_record;
    // Simulate reboot, then look up the actual key material and subscription.
    gopine_bond_init();union ble_store_key key={0};key.sec.peer_addr=our.sec.peer_addr;
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&key,&out));
    assert(!memcmp(&out.sec,&our.sec,sizeof(our.sec)));
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&key,&out));
    assert(!memcmp(&out.sec,&peer.sec,sizeof(peer.sec)));
    key.cccd.peer_addr=our.sec.peer_addr;key.cccd.chr_val_handle=27;
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_CCCD,&key,&out));
    assert(out.cccd.flags==1);
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&cccd));
    gopine_bond_poll(true);assert(next_record==written); // Identical subscriptions don't wear flash.
    // A middle unsubscribe must preserve the entire following indication
    // record, including pending Service Changed delivery, across reboot.
    union ble_store_value music=cccd,service_changed=cccd;
    music.cccd.chr_val_handle=36;
    service_changed.cccd.chr_val_handle=8;service_changed.cccd.flags=2;
    service_changed.cccd.value_changed=1;
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&music));
    assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_CCCD,&service_changed));
    key.cccd.chr_val_handle=36;
    assert(!ble_hs_cfg.store_delete_cb(BLE_STORE_OBJ_TYPE_CCCD,&key));
    key.cccd.chr_val_handle=8;
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_CCCD,&key,&out));
    assert(!memcmp(&out.cccd,&service_changed.cccd,sizeof(out.cccd)));
    gopine_bond_poll(true);gopine_bond_init();
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_CCCD,&key,&out));
    assert(!memcmp(&out.cccd,&service_changed.cccd,sizeof(out.cccd)));
    key.sec.peer_addr=our.sec.peer_addr;
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&key,&out));
    assert(!memcmp(&out.sec,&our.sec,sizeof(out.sec)));
    assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&key,&out));
    assert(!memcmp(&out.sec,&peer.sec,sizeof(out.sec)));
    assert(gopine_bond_forget());gopine_bond_init();
    assert(gopine_bond_status()==0);
    key.sec.peer_addr=our.sec.peer_addr;
    assert(ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&key,&out)==BLE_HS_ENOENT);

    multiple_peers(&our,&peer);

    // Older snapshots can contain weak, incomplete or mismatched key records.
    // Report a distinct reset-needed state and preserve every stored byte until
    // Forget; a reboot must not silently discard or call these records Saved.
    for(unsigned mode=0;mode<7;mode++) {
        assert(gopine_bond_forget());
        union ble_store_value invalid_our=our,invalid_peer=peer;
        if(mode==0)invalid_our.sec.authenticated=0;
        if(mode==1)invalid_peer.sec.key_size=12;
        if(mode==4)invalid_our.sec.sc=0;
        if(mode==5)invalid_peer.sec.ltk_present=0;
        if(mode==6)invalid_peer.sec.peer_addr.val[0]++;
        if(mode!=2)assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&invalid_our));
        if(mode!=3)assert(!ble_hs_cfg.store_write_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&invalid_peer));
        assert(gopine_bond_status()==-3 && gopine_bond_exists());
        gopine_bond_poll(true);
        uint32_t before[sizeof(flash)/sizeof(flash[0])];memcpy(before,flash,sizeof(flash));
        gopine_bond_init();
        assert(gopine_bond_status()==-3 && gopine_bond_exists());
        assert(!memcmp(before,flash,sizeof(flash)));
        if(mode!=2) {
            key.sec.peer_addr=invalid_our.sec.peer_addr;
            assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_OUR_SEC,&key,&out));
            assert(!memcmp(&out.sec,&invalid_our.sec,sizeof(out.sec)));
        }
        if(mode!=3) {
            key.sec.peer_addr=invalid_peer.sec.peer_addr;
            assert(!ble_hs_cfg.store_read_cb(BLE_STORE_OBJ_TYPE_PEER_SEC,&key,&out));
            assert(!memcmp(&out.sec,&invalid_peer.sec,sizeof(out.sec)));
        }
    }
    assert(gopine_bond_forget());assert(gopine_bond_status()==0 && !gopine_bond_exists());
}
void ble_hs_log_flat_buf(const void *data,int len){(void)data;(void)len;}
