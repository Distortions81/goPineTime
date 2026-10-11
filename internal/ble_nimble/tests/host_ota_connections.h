// OTA coexists with a saved phone on a separate real host connection. The fake
// controller supplies HCI events and shared ACL credits, never GAP callbacks.
static void ota_clear_replies(void) { reply_len=captured_count=0; }
static void disconnect_peer_on(uint16_t handle) {
    uint8_t event[]={BLE_HCI_EVCODE_DISCONN_CMP,4,0,0,0,BLE_ERR_REM_USER_CONN_TERM};
    put_le16(event+3,handle);send_event(event,sizeof(event));gopine_ble_poll();
    struct ble_gap_conn_desc desc;assert(ble_gap_conn_find(handle,&desc)==BLE_HS_ENOTCONN);
}
static void ota_subscribe_on(uint16_t handle,uint16_t status,bool enabled) {
    uint8_t request[]={BLE_ATT_OP_WRITE_REQ,0,0,0,0};
    put_le16(request+1,status+1);request[3]=enabled;
    att_on(handle,request,sizeof(request),BLE_ATT_OP_WRITE_RSP);
}
static void ota_status_on(uint16_t handle,uint16_t status,uint8_t payload_size) {
    uint8_t request[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(request+1,status);
    att_on(handle,request,sizeof(request),BLE_ATT_OP_READ_RSP);
    assert(reply_len>=25 && reply[9]==2 && reply[12]==payload_size);
}
static void ota_write_on(uint16_t handle,uint16_t attribute,const uint8_t *data,
                         unsigned size,uint8_t response) {
    uint8_t request[247]={BLE_ATT_OP_WRITE_REQ};assert(size<=244);
    put_le16(request+1,attribute);memcpy(request+3,data,size);
    att_on(handle,request,size+3,response);
}
static unsigned ota_notifications(uint16_t owner,uint16_t status,uint8_t capacity) {
    unsigned found=0;
    for(unsigned i=0;i<captured_count;i++) {
        const struct captured_pdu *pdu=&captured[i];
        if(pdu->size>=11 && get_le16(pdu->data+6)==BLE_L2CAP_CID_ATT &&
           pdu->data[8]==BLE_ATT_OP_NOTIFY_REQ && get_le16(pdu->data+9)==status) {
            assert(pdu->connection==owner && pdu->size==27 && pdu->data[14]==capacity);
            found++;
        }
    }
    return found;
}
static void ota_publish(uint16_t owner,uint16_t status,uint8_t state,uint32_t offset) {
    uint8_t snapshot[16]={2,state,0,0};
    put_le32(snapshot+4,236);put_le32(snapshot+8,offset);put_le32(snapshot+12,42);
    ota_clear_replies();gopine_ble_update_status(snapshot);gopine_ble_poll();
    assert(ota_notifications(owner,status,244)==1);
}
static void ota_mtu_on(uint16_t handle,uint16_t mtu) {
    uint8_t request[]={BLE_ATT_OP_MTU_REQ,0,0};put_le16(request+1,mtu);
    att_on(handle,request,sizeof(request),BLE_ATT_OP_MTU_RSP);
    assert(ble_att_mtu(handle)==mtu);
}
static void ota_assert_mailbox(const struct update_mailbox *expected) {
    assert(!memcmp(&update_incoming,expected,sizeof(*expected)));
}
static void ota_shared_credits(const uint8_t phone[6],const uint8_t pc[6],
                               uint16_t control,uint16_t data,uint16_t status) {
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    connect_peer_on(1,phone);assert(ble_gap_adv_active());connect_peer_on(2,pc);
    ota_mtu_on(1,185);ota_mtu_on(2,247);ota_subscribe_on(2,status,true);
    uint8_t begin[]={1,42,0,0,0,236,0,0,0},out[244];unsigned size;
    ota_write_on(2,control,begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_update(out,&size)==1);ota_publish(2,status,2,0);
    while(trace_count<24)trace_pairing(13,0x1007);
    uint16_t trace;assert(!ble_gatts_find_chr(&pairing_trace_service.u,
                                          &pairing_trace_characteristic.u,NULL,&trace));
    uint8_t request[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(request+1,trace);
    defer_acl=true;
    receive_l2cap_on(1,BLE_L2CAP_CID_ATT,request,sizeof(request));
    assert(pending_acl_count==3); // One spectator uses three of FOUR shared credits.
    uint8_t write[]={BLE_ATT_OP_WRITE_REQ,0,0,42,0,0,0,0,0,0,0,0x5a};
    put_le16(write+1,data);
    receive_l2cap_on(2,BLE_L2CAP_CID_ATT,write,sizeof(write));
    assert(pending_acl_count==4 && gopine_ble_take_update(out,&size)==2 && size==9);
    uint8_t snapshot[16]={2,2,0,0};put_le32(snapshot+4,236);
    put_le32(snapshot+8,1);put_le32(snapshot+12,42);
    gopine_ble_update_status(snapshot);gopine_ble_poll();
    assert(pending_acl_count==4); // No imaginary per-peer extra credit pool.
    defer_acl=false;ota_clear_replies();
    for(unsigned i=0;i<pending_acl_count;i++)assert(!ble_ll_hci_acl_rx(pending_acl[i],NULL));
    pending_acl_count=0;gopine_ble_poll();
    bool phone_response=false,pc_response=false;
    for(unsigned i=0;i<captured_count;i++) {
        if(captured[i].connection==1 && captured[i].data[8]==BLE_ATT_OP_READ_RSP)
            phone_response=true;
        if(captured[i].connection==2 && captured[i].data[8]==BLE_ATT_OP_WRITE_RSP)
            pc_response=true;
    }
    assert(phone_response && pc_response && ota_notifications(2,status,244)==1);
    // A second owner request proves its completion credit was returned to2.
    ota_status_on(2,status,244);assert(get_le32(reply+17)==1);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
}
static void ota_verification_wait(const uint8_t pc[6],uint16_t status) {
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    uint8_t snapshot[16]={2,3,0,0};put_le32(snapshot+4,236);
    put_le32(snapshot+8,236);put_le32(snapshot+12,42);
    gopine_ble_update_status(snapshot);
    connect_peer_on(2,pc);ota_mtu_on(2,247);ota_subscribe_on(2,status,true);
    struct ble_gap_conn_desc desc;
    test_rtc0.COUNTER+=16*32768;gopine_ble_poll();
    assert(!ble_gap_conn_find(2,&desc)); // Sender waits 45s for validation without issuing a claim.
    ota_publish(2,status,4,236);
    test_rtc0.COUNTER+=45*32768;gopine_ble_poll();gopine_ble_poll();
    assert(ble_gap_conn_find(2,&desc)==BLE_HS_ENOTCONN && ble_gap_adv_active());
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
}
static void ota_connections(const uint8_t *saved_ltk) {
    struct ble_store_key_sec key={.peer_addr=*BLE_ADDR_ANY};
    struct ble_store_value_sec our,peer;
    assert(!ble_store_read_our_sec(&key,&our));key.peer_addr=our.peer_addr;
    assert(!ble_store_read_peer_sec(&key,&peer));
    assert(!memcmp(saved_ltk,our.ltk,16));
    uint8_t previous_address[6];memcpy(previous_address,peer_address,6);
    const uint8_t pc_address[]={6,5,4,3,2,0xc1};
    uint16_t control,data,status;
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    uint8_t begin[]={1,42,0,0,0,236,0,0,0};
    uint8_t chunk[244]={42,0,0,0,0,0,0,0};memset(chunk+8,0x5a,236);
    uint8_t verify[]={2,42,0,0,0},out[244];unsigned size;
    struct ble_gap_conn_desc desc;
    capture_connections=true;
    for(unsigned owner_first=0;owner_first<2;owner_first++) {
        assert(!gopine_ble_start_update(80));gopine_ble_poll();
        assert(ble_gap_adv_active());
        connect_peer_on(1,our.peer_addr.val);
        assert(!ble_gap_conn_find(1,&desc) && !ble_addr_cmp(&desc.peer_id_addr,&our.peer_addr));
        request_encryption_on(1,saved_ltk);
        assert(!ble_gap_conn_find(1,&desc) && desc.sec_state.bonded && desc.sec_state.encrypted);
        // This fails with the old single-connection capacity/advertising policy.
        assert(ble_gap_adv_active());
        connect_peer_on(2,pc_address);
        assert(!ble_gap_conn_find(1,&desc) && !ble_gap_conn_find(2,&desc));
        assert(!desc.sec_state.encrypted && !desc.sec_state.bonded);
        assert(ble_gap_adv_active()); // A third OTA slot remains for another saved phone or uploader.
        ota_mtu_on(1,185);ota_mtu_on(2,247);
        ota_status_on(1,status,182);ota_status_on(2,status,244);
        ota_subscribe_on(1,status,true); // A subscription alone never owns OTA.
        ota_write_on(1,control,begin,8,BLE_ATT_OP_ERROR_RSP); // Invalid BEGIN cannot claim.
        assert(!update_incoming.count);
        ota_subscribe_on(2,status,true);
        ota_write_on(2,control,begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_update(out,&size)==1 && size==sizeof(begin) && !memcmp(out,begin,size));
        ota_publish(2,status,2,0);
        ota_write_on(2,data,chunk,sizeof(chunk),BLE_ATT_OP_WRITE_RSP);
        struct update_mailbox pending_update=update_incoming;
        assert(pending_update.count==1 && pending_update.packets[pending_update.head].kind==2);
        ota_write_on(1,control,begin,sizeof(begin),BLE_ATT_OP_ERROR_RSP);
        assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED);ota_assert_mailbox(&pending_update);
        ota_write_on(1,data,chunk,9,BLE_ATT_OP_ERROR_RSP);
        assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED);ota_assert_mailbox(&pending_update);
        ota_write_on(1,control,verify,sizeof(verify),BLE_ATT_OP_ERROR_RSP);
        assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED);ota_assert_mailbox(&pending_update);
        ota_subscribe_on(1,status,false);ota_subscribe_on(1,status,true);
        ota_assert_mailbox(&pending_update);
        // A real controller security event on the spectator cannot clear the
        // PC's transfer or change which connection receives status updates.
        uint8_t unencrypted[]={BLE_HCI_EVCODE_ENCRYPT_CHG,4,0,1,0,0};
        send_event(unencrypted,sizeof(unencrypted));gopine_ble_poll();
        ota_assert_mailbox(&pending_update);
        request_encryption_on(1,saved_ltk);ota_assert_mailbox(&pending_update);
        if(!owner_first) {
            disconnect_peer_on(1);ota_assert_mailbox(&pending_update);
            assert(!ble_gap_conn_find(2,&desc));
            assert(gopine_ble_take_update(out,&size)==2 && size==244 && !memcmp(out,chunk,size));
            ota_publish(2,status,2,236);
            disconnect_peer_on(2);assert(ble_gap_adv_active());
            connect_peer_on(2,pc_address);ota_mtu_on(2,247);ota_subscribe_on(2,status,true);
            // A complete staged image permits only matching VERIFY to resume.
            verify[1]=43;ota_write_on(2,control,verify,sizeof(verify),BLE_ATT_OP_ERROR_RSP);
            verify[1]=42;ota_write_on(2,control,verify,sizeof(verify),BLE_ATT_OP_WRITE_RSP);
            assert(gopine_ble_take_update(out,&size)==1 && size==5 && out[0]==2);
        } else {
            disconnect_peer_on(2);assert(!update_incoming.count && ble_gap_adv_active());
            assert(!ble_gap_conn_find(1,&desc));
            // No durable bytes were acknowledged: the reconnected PC can
            // claim the staged token at offset zero without a new BEGIN.
            connect_peer_on(2,pc_address);ota_mtu_on(2,247);ota_subscribe_on(2,status,true);
            chunk[0]=43;ota_write_on(2,data,chunk,9,BLE_ATT_OP_ERROR_RSP);chunk[0]=42;
            ota_write_on(2,data,chunk,sizeof(chunk),BLE_ATT_OP_WRITE_RSP);
            assert(gopine_ble_take_update(out,&size)==2 && size==244 && !memcmp(out,chunk,size));
            ota_publish(2,status,2,236);
            // A quiet spectator deadline must wake the normal Go wait loop;
            // it cannot require further phone traffic to free its slot.
            ota_clear_replies();gopine_ble_ack_updates();
            test_rtc0.COUNTER+=(UPDATE_CANDIDATE_GRACE_MS+1)*32768ull/1000;
            gopine_ble_controller_poll();assert(gopine_ble_updates());
            assert(gopine_ble_idle_ms()==0);gopine_ble_poll();gopine_ble_poll();
            assert(ble_gap_conn_find(1,&desc)==BLE_HS_ENOTCONN && !ble_gap_conn_find(2,&desc));
            ota_write_on(2,control,verify,sizeof(verify),BLE_ATT_OP_WRITE_RSP);
            assert(gopine_ble_take_update(out,&size)==1 && size==5);
        }
        gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();
        assert(!gopine_ble_busy() && ble_gap_conn_find(1,&desc)==BLE_HS_ENOTCONN &&
               ble_gap_conn_find(2,&desc)==BLE_HS_ENOTCONN);
        service_changed_keys_unchanged(&our,&peer);
    }
    ota_shared_credits(our.peer_addr.val,pc_address,control,data,status);
    ota_verification_wait(pc_address,status);
    service_changed_keys_unchanged(&our,&peer);
    // OTA never replaces the saved phone's keys or its ability to restore.
    uint8_t time[10]={0};assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    connect_peer_on(1,our.peer_addr.val);
    request_encryption_on(1,saved_ltk);assert_authenticated();
    assert(ble_gap_conn_find(2,&desc)==BLE_HS_ENOTCONN);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    service_changed_keys_unchanged(&our,&peer);
    capture_connections=false;memcpy(peer_address,previous_address,6);
    puts("two OTA connections: saved phone coexistence, per-link MTU, ownership, disconnect, resume and timeout passed");
}

static void ota_two_saved_phones(const struct ble_store_value_sec *first,
                                 const struct ble_store_value_sec *second) {
    const uint8_t pc[]={5,4,3,2,1,0xc3};
    capture_connections=true;client_request_count=0;
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    connect_peer_on(1,first->peer_addr.val);request_encryption_on(1,first->ltk);
    connect_peer_on(2,second->peer_addr.val);request_encryption_on(2,second->ltk);
    assert(ble_gap_adv_active()); // Both saved phones leave actual capacity for the PC.
    connect_peer_on(3,pc);assert(!ble_gap_adv_active());
    ota_mtu_on(1,185);ota_mtu_on(2,64);ota_mtu_on(3,247);
    uint16_t control,status;
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    ota_subscribe_on(1,status,true);ota_subscribe_on(2,status,true);ota_subscribe_on(3,status,true);
    uint8_t begin[]={1,42,0,0,0,236,0,0,0},out[244];unsigned size;
    ota_write_on(3,control,begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
    struct update_mailbox expected=update_incoming;
    for(unsigned phone=1;phone<=2;phone++) {
        ota_write_on(phone,control,begin,sizeof(begin),BLE_ATT_OP_ERROR_RSP);
        assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED);ota_assert_mailbox(&expected);
    }
    assert(gopine_ble_take_update(out,&size)==1 && size==sizeof(begin));
    ota_publish(3,status,2,0);
    uint16_t trace,data;
    assert(!ble_gatts_find_chr(&pairing_trace_service.u,&pairing_trace_characteristic.u,NULL,&trace));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    while(trace_count<24)trace_pairing(13,0x1007);
    uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,trace);
    defer_acl=true;receive_l2cap_on(1,BLE_L2CAP_CID_ATT,read,sizeof(read));
    assert(pending_acl_count==3);
    receive_l2cap_on(2,BLE_L2CAP_CID_ATT,read,sizeof(read));
    assert(pending_acl_count==4); // The second response waits for the same four credits.
    uint8_t write[]={BLE_ATT_OP_WRITE_REQ,0,0,42,0,0,0,0,0,0,0,0x5a};put_le16(write+1,data);
    receive_l2cap_on(3,BLE_L2CAP_CID_ATT,write,sizeof(write));
    assert(pending_acl_count==4 && gopine_ble_take_update(out,&size)==2 && size==9);
    uint8_t snapshot[16]={2,2,0,0};put_le32(snapshot+4,236);put_le32(snapshot+8,1);put_le32(snapshot+12,42);
    gopine_ble_update_status(snapshot);gopine_ble_poll();assert(pending_acl_count==4);
    defer_acl=false;ota_clear_replies();
    for(unsigned i=0;i<pending_acl_count;i++)assert(!ble_ll_hci_acl_rx(pending_acl[i],NULL));
    pending_acl_count=0;gopine_ble_poll();
    unsigned responses=0;
    for(unsigned i=0;i<captured_count;i++) {
        const struct captured_pdu *pdu=&captured[i];
        if(pdu->data[8]==BLE_ATT_OP_READ_RSP && pdu->connection<=2)responses|=1u<<pdu->connection;
        if(pdu->data[8]==BLE_ATT_OP_WRITE_RSP && pdu->connection==3)responses|=8;
    }
    assert(responses==14 && ota_notifications(3,status,244)==1);
    // Keep an unconsumed PC packet through both saved-phone disconnects.
    uint8_t chunk[]={42,0,0,0,1,0,0,0,0x5b};
    ota_write_on(3,data,chunk,sizeof(chunk),BLE_ATT_OP_WRITE_RSP);expected=update_incoming;
    disconnect_peer_on(1);ota_assert_mailbox(&expected);
    disconnect_peer_on(2);ota_assert_mailbox(&expected);
    assert(gopine_ble_take_update(out,&size)==2 && size==sizeof(chunk));
    ota_publish(3,status,2,2);
    struct ble_gap_conn_desc desc;assert(!ble_gap_conn_find(3,&desc));
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    assert(ble_gap_conn_find(3,&desc)==BLE_HS_ENOTCONN);
    capture_connections=false;
    puts("three OTA connections: two restored phones cannot take PC ownership or block its advertising slot");
}
