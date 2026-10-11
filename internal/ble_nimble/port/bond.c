// Use the pinned upstream key matching and CCCD store, with a durable snapshot.
#include "ble_store_ram.c"
#include "bond_journal.h"
// Keep the original on-flash layout explicit: changing the live array sizes
// must not reinterpret or discard the phone paired before this firmware.
struct bond_state_v1 {
    uint32_t version, our_count, peer_count, cccd_count;
    struct ble_store_value_sec our[1], peer[1];
    struct ble_store_value_cccd cccd[4];
};
struct bond_state {
    uint32_t version, our_count, peer_count, cccd_count;
    struct ble_store_value_sec our[MYNEWT_VAL(BLE_STORE_MAX_BONDS)];
    struct ble_store_value_sec peer[MYNEWT_VAL(BLE_STORE_MAX_BONDS)];
    struct ble_store_value_cccd cccd[MYNEWT_VAL(BLE_STORE_MAX_CCCDS)];
};
_Static_assert(sizeof(struct bond_state_v1)==240,"v1 bond ABI changed");
_Static_assert(MYNEWT_VAL(BLE_STORE_MAX_BONDS)==2 && MYNEWT_VAL(BLE_STORE_MAX_CCCDS)==8,
               "v2 bond capacities changed; migrate the journal schema");
_Static_assert(sizeof(struct bond_state)==464,"v2 bond ABI changed");
_Static_assert(sizeof(struct bond_state)<=BOND_PAYLOAD_SIZE,"bond snapshot too large");
static struct bond_state snapshot;
static unsigned next_record;
static bool dirty, failed;
static uint32_t save_after;
static void capture(void) {
    memset(&snapshot,0,sizeof(snapshot));
    snapshot.version=2;
    snapshot.our_count=ble_store_ram_num_our_secs;
    snapshot.peer_count=ble_store_ram_num_peer_secs;
    snapshot.cccd_count=ble_store_ram_num_cccds;
    memcpy(snapshot.our,ble_store_ram_our_secs,snapshot.our_count*sizeof(snapshot.our[0]));
    memcpy(snapshot.peer,ble_store_ram_peer_secs,snapshot.peer_count*sizeof(snapshot.peer[0]));
    memcpy(snapshot.cccd,ble_store_ram_cccds,snapshot.cccd_count*sizeof(snapshot.cccd[0]));
}
static void changed(void) {
    // Bound durability from the first change: repeated CCCD writes from
    // another connected peer must not postpone newly paired keys forever.
    if(!dirty)save_after=ble_npl_time_get()+1000;
    dirty=true;
}
static int write_bond(int type, const union ble_store_value *value) {
    // A rejected extra subscription or peer is not a flash failure and must
    // not prevent the already accepted records from becoming durable.
    int rc=ble_store_ram_write(type,value);if(!rc)changed();return rc;
}
static int delete_record(void *records,unsigned size,int *count,int index,unsigned capacity) {
    if(*count<0 || (unsigned)*count>capacity)return BLE_HS_EINVAL;
    if(index<0)return BLE_HS_ENOENT;
    if(index>=*count)return BLE_HS_EINVAL;
    (*count)--;
    uint8_t *at=(uint8_t*)records+(unsigned)index*size;
    memmove(at,at+size,((unsigned)*count-(unsigned)index)*size);
    memset((uint8_t*)records+(unsigned)*count*size,0,size);
    return 0;
}
static int delete_bond(int type, const union ble_store_key *key) {
    int rc;
    if(type==BLE_STORE_OBJ_TYPE_CCCD) {
        // The pinned RAM store compacts by record count as a byte length.
        // Compact complete records for subscriptions AND multi-peer keys.
        if(ble_store_ram_num_cccds<0 ||
           ble_store_ram_num_cccds>MYNEWT_VAL(BLE_STORE_MAX_CCCDS))return BLE_HS_EINVAL;
        int index=ble_store_ram_find_cccd(&key->cccd);
        rc=delete_record(ble_store_ram_cccds,sizeof(ble_store_ram_cccds[0]),
                         &ble_store_ram_num_cccds,index,MYNEWT_VAL(BLE_STORE_MAX_CCCDS));
    } else if(type==BLE_STORE_OBJ_TYPE_OUR_SEC || type==BLE_STORE_OBJ_TYPE_PEER_SEC) {
        struct ble_store_value_sec *records=type==BLE_STORE_OBJ_TYPE_OUR_SEC?
            ble_store_ram_our_secs:ble_store_ram_peer_secs;
        int *count=type==BLE_STORE_OBJ_TYPE_OUR_SEC?
            &ble_store_ram_num_our_secs:&ble_store_ram_num_peer_secs;
        if(*count<0 || *count>MYNEWT_VAL(BLE_STORE_MAX_BONDS))return BLE_HS_EINVAL;
        int index=ble_store_ram_find_sec(&key->sec,records,*count);
        rc=delete_record(records,sizeof(*records),count,index,MYNEWT_VAL(BLE_STORE_MAX_BONDS));
    } else rc=BLE_HS_ENOTSUP;
    if(!rc)changed();return rc;
}
static bool restore_snapshot(void) {
    struct bond_record record;
    bool found=false;
    next_record=0;
    memset(&snapshot,0,sizeof(snapshot));
    for(unsigned at=0;at<BOND_PAGE_SIZE;at+=BOND_RECORD_SIZE) {
        bond_read_record(at,&record);
        bool blank=true;
        for(unsigned i=0;i<sizeof(record);i++)
            if(((const uint8_t*)&record)[i]!=0xff){blank=false;break;}
        if(!blank)next_record=at+BOND_RECORD_SIZE; // Skip torn records as before.
        uint32_t counts[4];memcpy(counts,record.payload,sizeof(counts));
        if(bond_record_valid(&record,sizeof(struct bond_state_v1)) &&
           counts[0]==1 && counts[1]<=1 && counts[2]<=1 && counts[3]<=4) {
            // memcpy avoids imposing native alignment on the journal payload.
            struct bond_state_v1 previous;memcpy(&previous,record.payload,sizeof(previous));
            memset(&snapshot,0,sizeof(snapshot));
            snapshot.version=1;snapshot.our_count=previous.our_count;
            snapshot.peer_count=previous.peer_count;snapshot.cccd_count=previous.cccd_count;
            memcpy(snapshot.our,previous.our,sizeof(previous.our));
            memcpy(snapshot.peer,previous.peer,sizeof(previous.peer));
            memcpy(snapshot.cccd,previous.cccd,sizeof(previous.cccd));
            found=true;
        } else if(bond_record_valid(&record,sizeof(snapshot)) && counts[0]==2 &&
                  counts[1]<=MYNEWT_VAL(BLE_STORE_MAX_BONDS) &&
                  counts[2]<=MYNEWT_VAL(BLE_STORE_MAX_BONDS) &&
                  counts[3]<=MYNEWT_VAL(BLE_STORE_MAX_CCCDS)) {
            memcpy(&snapshot,record.payload,sizeof(snapshot));found=true;
        }
    }
    return found;
}
void gopine_bond_init(void) {
    ble_store_ram_init();
    memset(ble_store_ram_our_secs,0,sizeof(ble_store_ram_our_secs));
    memset(ble_store_ram_peer_secs,0,sizeof(ble_store_ram_peer_secs));
    memset(ble_store_ram_cccds,0,sizeof(ble_store_ram_cccds));
    dirty=failed=false;
    if(restore_snapshot()) {
        ble_store_ram_num_our_secs=snapshot.our_count;
        ble_store_ram_num_peer_secs=snapshot.peer_count;
        ble_store_ram_num_cccds=snapshot.cccd_count;
        memcpy(ble_store_ram_our_secs,snapshot.our,sizeof(snapshot.our));
        memcpy(ble_store_ram_peer_secs,snapshot.peer,sizeof(snapshot.peer));
        memcpy(ble_store_ram_cccds,snapshot.cccd,sizeof(snapshot.cccd));
    }
    ble_hs_cfg.store_write_cb=write_bond;ble_hs_cfg.store_delete_cb=delete_bond;
    // Migration stays readable without any immediate erase or key rewrite.
    // The next changed snapshot (or normal offline reclamation) uses v2.
    capture();
}
// Reserve space before radio startup. Losing power during page reclamation can
// lose the bond; it cannot restore partial keys or touch firmware/settings.
bool gopine_bond_prepare(uint8_t battery) {
    if(next_record < BOND_PAGE_SIZE-BOND_RECORD_SIZE && !failed)return true;
    if(battery<20)return false;
    capture();
    if(!bond_flash_erase())return false;
    next_record=0;failed=!bond_journal_save(&snapshot,sizeof(snapshot),&next_record);
    dirty=false;return !failed;
}
void gopine_bond_poll(bool stopped) {
    if(!dirty || failed || (!stopped && (int32_t)(ble_npl_time_get()-save_after)<0))return;
    // Repeated CCCD writes with identical values must not wear the journal.
    uint32_t before=bond_crc(&snapshot,sizeof(snapshot));capture();dirty=false;
    if(before!=bond_crc(&snapshot,sizeof(snapshot)))
        failed=!bond_journal_save(&snapshot,sizeof(snapshot),&next_record);
}
static bool strong_key(const struct ble_store_value_sec *key) {
    return key->ltk_present && key->authenticated && key->sc && key->key_size==16;
}
static int key_index(const ble_addr_t *addr,const struct ble_store_value_sec *keys,int count) {
    struct ble_store_key_sec key={.peer_addr=*addr};
    return ble_store_ram_find_sec(&key,keys,count);
}
static bool saved_peer(const ble_addr_t *addr) {
    int our=key_index(addr,ble_store_ram_our_secs,ble_store_ram_num_our_secs);
    int peer=key_index(addr,ble_store_ram_peer_secs,ble_store_ram_num_peer_secs);
    return our>=0 && peer>=0 && strong_key(&ble_store_ram_our_secs[our]) &&
           strong_key(&ble_store_ram_peer_secs[peer]);
}
unsigned gopine_bond_count(void) {
    unsigned count=0;
    for(int i=0;i<ble_store_ram_num_our_secs;i++) {
        const ble_addr_t *addr=&ble_store_ram_our_secs[i].peer_addr;
        if(key_index(addr,ble_store_ram_our_secs,i)<0 && saved_peer(addr))count++;
    }
    return count;
}
bool gopine_bond_peer_saved(uint16_t conn) {
    struct ble_gap_conn_desc desc;
    return !ble_gap_conn_find(conn,&desc) && saved_peer(&desc.peer_id_addr);
}
bool gopine_bond_can_pair(uint16_t conn) {
    struct ble_gap_conn_desc desc;
    if(failed || ble_gap_conn_find(conn,&desc) ||
       ble_store_ram_num_our_secs>=MYNEWT_VAL(BLE_STORE_MAX_BONDS) ||
       ble_store_ram_num_peer_secs>=MYNEWT_VAL(BLE_STORE_MAX_BONDS) ||
       key_index(&desc.peer_id_addr,ble_store_ram_our_secs,ble_store_ram_num_our_secs)>=0 ||
       key_index(&desc.peer_id_addr,ble_store_ram_peer_secs,ble_store_ram_num_peer_secs)>=0)
        return false;
    // An incomplete record still reserves its identity until explicit Forget.
    unsigned reserved=(unsigned)ble_store_ram_num_our_secs;
    for(int i=0;i<ble_store_ram_num_peer_secs;i++)
        if(key_index(&ble_store_ram_peer_secs[i].peer_addr,ble_store_ram_our_secs,
                     ble_store_ram_num_our_secs)<0)reserved++;
    return reserved<MYNEWT_VAL(BLE_STORE_MAX_BONDS);
}
int gopine_bond_status(void) {
    if(failed)return -1;
    // Validate every peer, independently of insertion order. Preserve unusable
    // old records for explicit Forget; never call a partially saved pair Saved.
    unsigned count=gopine_bond_count();
    if(count!=(unsigned)ble_store_ram_num_our_secs ||
       count!=(unsigned)ble_store_ram_num_peer_secs)return -3;
    if(dirty)return 1;
    return count?2:0;
}
bool gopine_bond_exists(void) {
    // Retained for aggregate diagnostics; pairing decisions are peer-specific.
    return ble_store_ram_num_our_secs!=0 || ble_store_ram_num_peer_secs!=0;
}
uint32_t gopine_bond_delay(void) {
    if(!dirty || failed)return 240000;
    int32_t left=(int32_t)(save_after-ble_npl_time_get());return left>0?(uint32_t)left:0;
}
bool gopine_bond_forget(void) {
    if(!bond_flash_erase()){failed=true;return false;}
    memset(ble_store_ram_our_secs,0,sizeof(ble_store_ram_our_secs));
    memset(ble_store_ram_peer_secs,0,sizeof(ble_store_ram_peer_secs));
    memset(ble_store_ram_cccds,0,sizeof(ble_store_ram_cccds));
    ble_store_ram_num_our_secs=ble_store_ram_num_peer_secs=ble_store_ram_num_cccds=0;
    next_record=0;dirty=failed=false;capture();return true;
}
