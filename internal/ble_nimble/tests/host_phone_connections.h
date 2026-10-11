extern unsigned gopine_bond_count(void);
// Establish the second bond through the same real SMP host used for the first.
// No security flags, key-store records or GAP callbacks are injected directly.
static void pair_second_phone(uint16_t handle,const uint8_t identity_address[6],
                              uint8_t ltk_out[16]) {
    uint8_t pairing[]={BLE_SM_OP_PAIR_REQ,BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,16,3,3};
    receive_l2cap_on(handle,BLE_L2CAP_CID_SM,pairing,sizeof(pairing));
    assert(reply[8]==BLE_SM_OP_PAIR_RSP && (get_le16(reply)&0x0fff)==handle);
    uint8_t response[7];memcpy(response,reply+8,sizeof(response));
    uint8_t private_be[32]={0},private_le[32]={7},public_be[64];private_be[31]=7;
    assert(uECC_compute_public_key(private_be,public_be,uECC_secp256r1()));
    uint8_t public[65]={BLE_SM_OP_PAIR_PUBLIC_KEY},watch_public[64];
    for(unsigned i=0;i<32;i++){public[1+i]=public_be[31-i];public[33+i]=public_be[63-i];}
    receive_l2cap_on(handle,BLE_L2CAP_CID_SM,public,sizeof(public));
    assert(reply[8]==BLE_SM_OP_PAIR_PUBLIC_KEY && gopine_ble_pairing_code()>0);
    memcpy(watch_public,reply+9,sizeof(watch_public));
    uint32_t passkey=gopine_ble_pairing_code()-1;
    uint8_t nonce[16],watch_nonce[16],confirm[17],random[17],watch_confirm[16];
    for(unsigned round=0;round<20;round++) {
        memset(nonce,round+31,16);uint8_t bit=0x80|((passkey>>round)&1);
        confirm[0]=BLE_SM_OP_PAIR_CONFIRM;
        assert(!ble_sm_alg_f4(public+1,watch_public,nonce,bit,confirm+1));
        receive_l2cap_on(handle,BLE_L2CAP_CID_SM,confirm,sizeof(confirm));
        assert(reply[8]==BLE_SM_OP_PAIR_CONFIRM);memcpy(watch_confirm,reply+9,16);
        random[0]=BLE_SM_OP_PAIR_RANDOM;memcpy(random+1,nonce,16);
        receive_l2cap_on(handle,BLE_L2CAP_CID_SM,random,sizeof(random));
        assert(reply[8]==BLE_SM_OP_PAIR_RANDOM);memcpy(watch_nonce,reply+9,16);
        uint8_t expected[16];assert(!ble_sm_alg_f4(watch_public,public+1,watch_nonce,bit,expected));
        assert(!memcmp(expected,watch_confirm,16));
    }
    struct ble_gap_conn_desc desc;assert(!ble_gap_conn_find(handle,&desc));
    uint8_t dhkey[32],mackey[16],tk[16]={0},check[17]={BLE_SM_OP_PAIR_DHKEY_CHECK};
    put_le32(tk,passkey);
    assert(!ble_sm_alg_gen_dhkey(watch_public,watch_public+32,private_le,dhkey));
    assert(!ble_sm_alg_f5(dhkey,nonce,watch_nonce,desc.peer_ota_addr.type,desc.peer_ota_addr.val,
                         desc.our_ota_addr.type,desc.our_ota_addr.val,mackey,ltk_out));
    assert(!ble_sm_alg_f6(mackey,nonce,watch_nonce,tk,pairing+1,
                         desc.peer_ota_addr.type,desc.peer_ota_addr.val,
                         desc.our_ota_addr.type,desc.our_ota_addr.val,check+1));
    receive_l2cap_on(handle,BLE_L2CAP_CID_SM,check,sizeof(check));
    assert(reply[8]==BLE_SM_OP_PAIR_DHKEY_CHECK);
    uint8_t expected[16];
    assert(!ble_sm_alg_f6(mackey,watch_nonce,nonce,tk,response+1,
                         desc.our_ota_addr.type,desc.our_ota_addr.val,
                         desc.peer_ota_addr.type,desc.peer_ota_addr.val,expected));
    assert(!memcmp(reply+9,expected,16));
    request_encryption_on(handle,ltk_out);
    assert(!ble_gap_conn_find(handle,&desc) && desc.sec_state.encrypted &&
           desc.sec_state.authenticated && !desc.sec_state.bonded);
    uint8_t identity[17]={BLE_SM_OP_IDENTITY_INFO};memset(identity+1,0x66,16);
    receive_l2cap_on(handle,BLE_L2CAP_CID_SM,identity,sizeof(identity));
    uint8_t address[8]={BLE_SM_OP_IDENTITY_ADDR_INFO,BLE_ADDR_RANDOM};
    memcpy(address+2,identity_address,6);
    receive_l2cap_on(handle,BLE_L2CAP_CID_SM,address,sizeof(address));
    assert(!ble_gap_conn_find(handle,&desc) && desc.sec_state.encrypted &&
           desc.sec_state.authenticated && desc.sec_state.bonded && desc.sec_state.key_size==16);
    assert(!memcmp(desc.peer_id_addr.val,identity_address,6));
}
static void phone_music_subscribe_on(uint16_t handle,bool enabled) {
    uint16_t cccd;assert(!ble_gatts_find_dsc(&music_uuids[0].u,&music_uuids[1].u,
                                          BLE_UUID16_DECLARE(0x2902),&cccd));
    uint8_t request[]={BLE_ATT_OP_WRITE_REQ,0,0,0,0};put_le16(request+1,cccd);request[3]=enabled;
    att_on(handle,request,sizeof(request),BLE_ATT_OP_WRITE_RSP);
}
static void phone_music_control_on(uint16_t handle,uint32_t generation) {
    ota_clear_replies();assert(!gopine_ble_music_command(0,generation));gopine_ble_poll();
    assert(captured_count==1 && captured[0].connection==handle && captured[0].data[8]==BLE_ATT_OP_NOTIFY_REQ &&
           get_le16(captured[0].data+9)==music_handle && captured[0].data[11]==0);
}
static uint8_t phone_apple_sources[3];
static void phone_two_ancs(void) {
    retain_apple=true;memset(apple_servers,0,sizeof(apple_servers));
    apple_servers[0].available=apple_servers[1].available=true;
    apple_hold_attributes=true;apple_held_count=0;apple_drain();
    assert(apple_held_count==2 && apple_held_attributes[0].connection!=apple_held_attributes[1].connection);
    uint8_t attributes[2][151]={{0}};
    for(unsigned i=0;i<2;i++) {
        const struct client_request *request=&apple_held_attributes[i];
        memcpy(attributes[i]+1,request->data+4,4);
        attributes[i][5]=1;put_le16(attributes[i]+6,40);memset(attributes[i]+8,'S'+request->connection,40);
        attributes[i][48]=3;put_le16(attributes[i]+49,100);memset(attributes[i]+51,'B',100);
        apple_connection=request->connection;
        const uint8_t response=BLE_ATT_OP_WRITE_RSP;apple_att(&response,1);
    }
    // Both real GATT attribute requests are live. Interleave their full-sized
    // tuple streams over ATT so shared-pool pressure and parser routing coexist.
    for(unsigned offset=0;offset<151;offset+=17)for(unsigned i=0;i<2;i++) {
        unsigned size=151-offset;if(size>17)size=17;
        apple_connection=apple_held_attributes[i].connection;
        apple_notify(31,attributes[i]+offset,size,false);
    }
    apple_hold_attributes=false;apple_held_count=0;
    assert(apple_servers[0].source_subscribed && apple_servers[1].source_subscribed);
    assert(apple_servers[0].attributes==1 && apple_servers[1].attributes==1);
    uint8_t out[ANCS_PACKET_SIZE];unsigned seen=0;
    while(gopine_ble_take_apple_notification(out)) {
        assert(out[147]>=1 && out[147]<=2);
        if(out[4]==3)continue;
        assert(out[4]==0 && get_le32(out)==0x12345678 && out[7]>='T' && out[7]<='U');
        unsigned handle=out[7]-'S';phone_apple_sources[handle]=out[147];seen|=1u<<out[147];
    }
    assert(seen==6 && phone_apple_sources[1]!=phone_apple_sources[2]); // Identical Apple UIDs from two ATT servers remain distinct.
    apple_connection=1;apple_source(0x12345678,2);
    assert(gopine_ble_take_apple_notification(out)==ANCS_PACKET_SIZE && out[4]==2 && out[147]==phone_apple_sources[1]);
    assert(!gopine_ble_take_apple_notification(out));
    apple_connection=2;apple_source(0x12345678,1);apple_drain();
    assert(gopine_ble_take_apple_notification(out)==ANCS_PACKET_SIZE && out[4]==1 && out[147]==phone_apple_sources[2]);
    assert(!gopine_ble_take_apple_notification(out));
}
static void phone_connections(const uint8_t first_ltk[16]) {
    struct ble_store_key_sec first_key={.peer_addr=*BLE_ADDR_ANY};
    struct ble_store_value_sec first_our,first_peer,second_our,second_peer;
    assert(!ble_store_read_our_sec(&first_key,&first_our));first_key.peer_addr=first_our.peer_addr;
    assert(!ble_store_read_peer_sec(&first_key,&first_peer));
    const uint8_t second_temporary[]={9,8,7,6,5,0x42},second_identity[]={9,8,7,6,5,0xc2};
    uint8_t time[10]={0},second_ltk[16],out[ANCS_PACKET_SIZE];uint32_t age;
    struct ble_gap_conn_desc desc;
    client_request_count=0;capture_connections=true;
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    connect_peer_on(1,first_our.peer_addr.val);request_encryption_on(1,first_ltk);
    assert(phone_link_ready(1) && ble_gap_adv_active());
    // Cancelling a newly displayed PIN closes only that applicant, retaining
    // the already authenticated phone and its original durable bond.
    connect_peer_on(2,second_temporary);
    uint8_t pair[]={BLE_SM_OP_PAIR_REQ,BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,16,3,3};
    receive_l2cap_on(2,BLE_L2CAP_CID_SM,pair,sizeof(pair));assert(reply[8]==BLE_SM_OP_PAIR_RSP);
    uint8_t private[32]={0},public[64],packet[65]={BLE_SM_OP_PAIR_PUBLIC_KEY};private[31]=7;
    assert(uECC_compute_public_key(private,public,uECC_secp256r1()));
    for(unsigned i=0;i<32;i++){packet[1+i]=public[31-i];packet[33+i]=public[63-i];}
    receive_l2cap_on(2,BLE_L2CAP_CID_SM,packet,sizeof(packet));assert(gopine_ble_pairing_code());
    assert(!gopine_ble_cancel_pairing());gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_pairing_code() && phone_link_ready(1) &&
           ble_gap_conn_find(2,&desc)==BLE_HS_ENOTCONN && ble_gap_adv_active());
    service_changed_keys_unchanged(&first_our,&first_peer);
    connect_peer_on(2,second_temporary);pair_second_phone(2,second_identity,second_ltk);
    assert(gopine_bond_count()==2 && gopine_ble_trusted_peer_count()==2 && !ble_gap_adv_active());
    assert(memcmp(first_ltk,second_ltk,16));
    struct ble_store_key_sec second_key={.peer_addr={.type=BLE_ADDR_RANDOM}};
    memcpy(second_key.peer_addr.val,second_identity,6);
    assert(!ble_store_read_our_sec(&second_key,&second_our));
    assert(!ble_store_read_peer_sec(&second_key,&second_peer));
    assert(!memcmp(second_ltk,second_our.ltk,16));service_changed_keys_unchanged(&first_our,&first_peer);
    gopine_ble_stop();test_rtc0.COUNTER+=32768;gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2);
    gopine_bond_init();assert(gopine_bond_count()==2 && gopine_bond_status()==2);
    // Reload both actual saved keys, then restore two independent host links.
    client_request_count=0;retain_apple=true;
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    connect_peer_on(1,first_our.peer_addr.val);connect_peer_on(2,second_identity);
    ota_mtu_on(1,185);ota_mtu_on(2,247);
    request_ltk_on(2,second_ltk);
    assert(gopine_sm_progress(1) && gopine_sm_progress(2) &&
           gopine_sm_progress(1)!=gopine_sm_progress(2));
    // Opposite-order restoration leaves two real SMP procedures in different
    // phases. A quiet poll must not alternate the aggregate progress/trace.
    for(unsigned i=0;i<8 && gopine_ble_host_work_pending();i++)gopine_ble_poll();
    while(gopine_ble_take_apple_notification(out)){}
    gopine_ble_take_music(out);gopine_ble_ack_updates();
    uint8_t quiet_trace[sizeof(pairing_trace)];memcpy(quiet_trace,pairing_trace,sizeof(quiet_trace));
    uint16_t quiet_progress=gopine_ble_pairing_progress();
    for(unsigned i=0;i<4;i++) {
        gopine_ble_poll();
        assert(!gopine_ble_updates() && gopine_ble_idle_ms()>0);
        assert(gopine_ble_pairing_progress()==quiet_progress &&
               !memcmp(quiet_trace,pairing_trace,sizeof(quiet_trace)));
    }
    encryption_complete_on(2);
    assert(phone_link_ready(2) && !phone_link_ready(1));
    write_characteristic_on(1,&music_uuids[0].u,&music_uuids[4].u,
                            companion_track,sizeof(companion_track),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_ENC);
    request_encryption_on(1,first_ltk);
    assert(gopine_ble_trusted_peer_count()==2 && !gopine_ble_pairing_code());
    phone_two_ancs();
    phone_music_subscribe_on(1,true);phone_music_subscribe_on(2,false);
    write_characteristic_on(1,&music_uuids[0].u,&music_uuids[4].u,
                            companion_track,sizeof(companion_track),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_music(out) && out[81]==PHONE_LINK_MUSIC_READY);
    uint32_t generation1=get_le32(out+83);assert(generation1);
    const uint8_t second_track[]="Second phone";
    write_characteristic_on(2,&music_uuids[0].u,&music_uuids[4].u,
                            second_track,sizeof(second_track),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_music(out) && out[81]==PHONE_LINK_CONNECTED &&
           !memcmp(out,second_track,sizeof(second_track)));
    uint32_t generation2=get_le32(out+83);assert(generation2 && generation2!=generation1);
    assert(gopine_ble_music_command(0,generation2)==BLE_HS_ENOTCONN);
    phone_music_control_on(1,generation1); // B's metadata cannot steal A's command destination.
    phone_music_subscribe_on(2,true);phone_music_control_on(2,generation2);
    // Queue independent feature payloads from each authenticated source.
    retain_peer_data=true;
    uint8_t time1[]={0xea,7,10,9,16,7,8,5,0,1},time2[]={0xea,7,10,9,17,8,9,5,0,1};
    write_characteristic_on(1,BLE_UUID16_DECLARE(0x1805),BLE_UUID16_DECLARE(0x2a2b),time1,10,BLE_ATT_OP_WRITE_RSP);
    write_characteristic_on(2,BLE_UUID16_DECLARE(0x1805),BLE_UUID16_DECLARE(0x2a2b),time2,10,BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take(out,&age)==10 && !memcmp(out,time1,10));
    assert(gopine_ble_take(out,&age)==10 && !memcmp(out,time2,10));
    write_characteristic_on(2,&weather_uuid.u,&weather_data_uuid.u,
                            companion_current_v1,sizeof(companion_current_v1),BLE_ATT_OP_WRITE_RSP);
    write_characteristic_on(2,BLE_UUID16_DECLARE(0x1811),BLE_UUID16_DECLARE(0x2a46),
                            companion_alert,sizeof(companion_alert),BLE_ATT_OP_WRITE_RSP);
    uint8_t disabled[]={BLE_HCI_EVCODE_ENCRYPT_CHG,4,0,1,0,0};
    send_event(disabled,sizeof(disabled));gopine_ble_poll();
    assert(!phone_link_ready(1) && phone_link_ready(2));
    assert(gopine_ble_take_weather(out)==sizeof(companion_current_v1) && !memcmp(out,companion_current_v1,sizeof(companion_current_v1)));
    assert(gopine_ble_take_notification(out)==sizeof(companion_alert) && !memcmp(out,companion_alert,sizeof(companion_alert)));
    disconnect_peer_on(1);assert(phone_link_ready(2) && ble_gap_adv_active());
    // ANCS teardown clears source A only; source B keeps serving attributes.
    bool cleared=false;
    while(gopine_ble_take_apple_notification(out)){assert(out[4]==3 && out[147]==phone_apple_sources[1]);cleared=true;}
    assert(cleared);
    apple_connection=2;apple_source(0x12345678,1);apple_drain();
    assert(gopine_ble_take_apple_notification(out)==ANCS_PACKET_SIZE && out[4]==1 && out[147]==phone_apple_sources[2]);
    phone_music_control_on(2,generation2);
    connect_peer_on(1,first_our.peer_addr.val);request_encryption_on(1,first_ltk);
    assert(phone_link_ready(1) && phone_link_ready(2));
    assert(gopine_ble_music_command(0,generation1)==BLE_HS_ENOTCONN); // Reused HCI handle, fresh source token.
    phone_music_control_on(2,generation2);
    retain_peer_data=retain_apple=false;capture_connections=false;apple_connection=1;
    gopine_ble_stop();test_rtc0.COUNTER+=32768;gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_count()==2);
    service_changed_keys_unchanged(&first_our,&first_peer);
    service_changed_keys_unchanged(&second_our,&second_peer);
    ota_two_saved_phones(&first_our,&second_our);
    service_changed_keys_unchanged(&first_our,&first_peer);
    service_changed_keys_unchanged(&second_our,&second_peer);
    puts("two saved phones: independent SC bonds, restore, MTU, security, ANCS, music routing, feature mailboxes and cancel isolation passed");
}
