// Real ATT Prepare / Execute traffic bounds its metadata by the existing mbuf
// pool, including empty fragments. No application callback is invoked directly.
static unsigned prepared_count(uint16_t handle) {
    struct ble_hs_conn *conn=ble_hs_conn_find(handle);assert(conn);
    struct ble_att_prep_entry *entry;unsigned count=0;
    SLIST_FOREACH(entry,&conn->bhc_att_svr.basc_prep_list,bape_next) {
        assert(entry->bape_value);count++;
    }
    return count;
}
static void prepare_fragment(uint16_t handle,uint16_t attribute,unsigned offset,
                              const uint8_t *data,unsigned size,uint8_t response) {
    uint8_t request[247]={BLE_ATT_OP_PREP_WRITE_REQ};assert(size<=242);
    put_le16(request+1,attribute);put_le16(request+3,offset);
    memcpy(request+5,data,size);att_on(handle,request,size+5,response);
    if(response==BLE_ATT_OP_PREP_WRITE_RSP) {
        assert(reply_len==size+13 && !memcmp(reply+9,request+1,size+4));
    }
}
static void execute_prepared(uint16_t handle,bool commit,uint8_t response) {
    const uint8_t request[]={BLE_ATT_OP_EXEC_WRITE_REQ,commit?1:0};
    att_on(handle,request,sizeof(request),response);
}
static void prepare_three(uint16_t handle,uint16_t attribute,const uint8_t data[9]) {
    for(unsigned offset=0;offset<9;offset+=3)
        prepare_fragment(handle,attribute,offset,data+offset,3,BLE_ATT_OP_PREP_WRITE_RSP);
    assert(prepared_count(handle)==3);
}
static void ota_prepared_writes(void) {
    _Static_assert(MYNEWT_VAL(MSYS_2_BLOCK_COUNT)==0,"Revisit prepared-write capacity for another msys pool");
    _Static_assert(MYNEWT_VAL(BLE_ATT_SVR_MAX_PREP_ENTRIES)==MYNEWT_VAL(MSYS_1_BLOCK_COUNT),
                   "Prepared-write entries must cover every available mbuf");
    const uint8_t pc[]={6,5,4,3,2,0xc1};
    uint16_t control,data,status;
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    connect_peer_on(1,peer_address);connect_peer_on(2,pc);
    ota_subscribe_on(1,status,true);
    assert(os_msys_num_free()==MYNEWT_VAL(MSYS_1_BLOCK_COUNT));
    const uint8_t begin[]={1,42,0,0,0,236,0,0,0};
    uint8_t out[244];unsigned size;

    // Seven queued fragments exhaust every mbuf except the incoming request /
    // response. The eighth metadata entry is still available; its mandatory
    // payload buffer is what fails, just as with the former64-entry pool.
    for(unsigned i=0;i<7;i++)
        prepare_fragment(1,control,i,begin+i,i==6?3:1,BLE_ATT_OP_PREP_WRITE_RSP);
    assert(prepared_count(1)==7 && os_msys_num_free()==1);
    prepare_fragment(2,control,0,begin,0,BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_RES && prepared_count(1)==7 && !prepared_count(2));
    assert(!update_incoming.count && update_links.owner==BLE_HS_CONN_HANDLE_NONE);
    execute_prepared(1,false,BLE_ATT_OP_EXEC_WRITE_RSP);
    assert(!prepared_count(1) && os_msys_num_free()==8 && !update_incoming.count);

    // Execute the same fragmented control command through the production
    // callback; only successful execution may establish OTA ownership.
    prepare_three(1,control,begin);execute_prepared(1,true,BLE_ATT_OP_EXEC_WRITE_RSP);
    gopine_ble_poll(); // Drain the central's reply to owner interval tuning.
    assert(!prepared_count(1) && update_links.owner==1 && os_msys_num_free()==8);
    assert(gopine_ble_take_update(out,&size)==1 && size==9 && !memcmp(out,begin,size));
    uint8_t snapshot[16]={2,2,0,0};put_le32(snapshot+4,236);put_le32(snapshot+12,42);
    gopine_ble_update_status(snapshot);gopine_ble_poll();
    const uint8_t chunk[]={42,0,0,0,0,0,0,0,0x5a};
    prepare_three(1,data,chunk);prepare_three(2,data,chunk);
    assert(os_msys_num_free()==2 && prepared_count(1)==3 && prepared_count(2)==3);
    disconnect_peer_on(2);
    assert(prepared_count(1)==3 && os_msys_num_free()==5 && update_links.owner==1);
    execute_prepared(1,true,BLE_ATT_OP_EXEC_WRITE_RSP);
    assert(os_msys_num_free()==8 && !prepared_count(1));
    assert(gopine_ble_take_update(out,&size)==2 && size==9 && !memcmp(out,chunk,size));
    put_le32(snapshot+8,1);gopine_ble_update_status(snapshot);gopine_ble_poll();
    // Disconnect also releases an owner's unexecuted entries without sending
    // their bytes to Go or changing the flash-confirmed status.
    uint8_t next[9];memcpy(next,chunk,sizeof(next));put_le32(next+4,1);
    prepare_three(1,data,next);disconnect_peer_on(1);
    assert(os_msys_num_free()==8 && !update_incoming.count && update_u32(update_status+8)==1);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    puts("ATT prepared writes: physical mbuf capacity, cancellation, execution and per-link disconnect passed");
}
