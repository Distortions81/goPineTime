static void mode_payload_quiet(void) {
    uint8_t out[ANCS_PACKET_SIZE];
    for(unsigned i=0;i<8 && gopine_ble_host_work_pending();i++)gopine_ble_poll();
    while(gopine_ble_take_apple_notification(out)){}
    gopine_ble_take_music(out);gopine_ble_ack_updates();
}
static void mode_payload_lifecycle(void) {
    // Numeric mailbox fields have the same layout on ARM and the native host.
    assert(sizeof(phone_peers)==64 && sizeof(mode_payload)==984);
    const uint8_t addresses[3][6]={{1,3,5,7,9,0xce},{2,4,6,8,10,0xce},{3,6,9,12,15,0xce}};
    uint16_t control,data,status,weather;
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    assert(!ble_gatts_find_chr(&weather_uuid.u,&weather_data_uuid.u,NULL,&weather));
    for(unsigned cycle=0;cycle<100;cycle++) {
        uint8_t time[10]={0},out[ANCS_PACKET_SIZE],saved[sizeof(mode_payload)];
        assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
        connect_peer_on(1,addresses[0]);connect_peer_on(2,addresses[1]);
        mode_payload_quiet();
        memset(&mode_payload,0xa5,sizeof(mode_payload));memcpy(saved,&mode_payload,sizeof(saved));
        assert(gopine_ble_start_update(80)==BLE_HS_EBUSY);
        assert(phone_window && !update_window && !memcmp(saved,&mode_payload,sizeof(saved)));
        uint8_t stale_status[16]={2,5,1};gopine_ble_update_status(stale_status);
        assert(!memcmp(saved,&mode_payload,sizeof(saved)));
        // Untrusted phone payloads cannot be interpreted as an OTA queue or
        // standalone weather update by the mode-independent wake predicate.
        assert(!gopine_ble_updates());
        disconnect_peer_on(1);
        const uint8_t zero[sizeof(struct phone_payload)]={0};
        assert(!memcmp(&mode_payload.phone[0],zero,sizeof(zero)));
        assert(!memcmp(&mode_payload.phone[1],saved+sizeof(struct phone_payload),sizeof(struct phone_payload)));
        gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());

        assert(!gopine_ble_start_weather(time,80,30000));gopine_ble_poll();connect_peer_on(1,addresses[0]);
        ota_mtu_on(1,247);
        ota_write_on(1,weather,companion_current_v1,sizeof(companion_current_v1),BLE_ATT_OP_WRITE_RSP);
        retain_peer_data=true;
        uint16_t music_status;
        assert(!ble_gatts_find_chr(&music_uuids[0].u,&music_uuids[2].u,NULL,&music_status));
        uint8_t read_music[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read_music+1,music_status);
        att_on(1,read_music,sizeof(read_music),BLE_ATT_OP_READ_RSP);
        assert(reply_len==10 && !reply[9]); // Weather bytes are never Music metadata.
        ota_subscribe_on(1,music_handle,true);
        disconnect_peer_on(1);connect_peer_on(2,addresses[1]);
        retain_peer_data=false;
        assert(gopine_ble_take_weather(out)==sizeof(companion_current_v1));
        assert(!memcmp(out,companion_current_v1,sizeof(companion_current_v1)));
        test_rtc0.COUNTER+=32768;
        gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());

        assert(!gopine_ble_start_update(80));gopine_ble_poll();connect_peer_on(3,addresses[2]);
        ota_mtu_on(3,247);ota_subscribe_on(3,status,true);
        uint8_t begin[]={1,42,0,0,0,236,0,0,0},packet[244];unsigned size;
        ota_write_on(3,control,begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_update(packet,&size)==1);
        uint8_t snapshot[16]={2,2};put_le32(snapshot+4,236);put_le32(snapshot+12,42);
        gopine_ble_update_status(snapshot);gopine_ble_poll();
        memset(packet,0x5a,sizeof(packet));put_le32(packet,42);put_le32(packet+4,0);
        ota_write_on(3,data,packet,sizeof(packet),BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_update(packet,&size)==2 && size==244);
        mode_payload_quiet();memcpy(saved,&mode_payload,sizeof(saved));
        uint8_t saved_status[16];memcpy(saved_status,update_status,sizeof(saved_status));
        assert(gopine_ble_start_update(80)==BLE_HS_EBUSY);
        assert(update_window && !memcmp(saved_status,update_status,sizeof(saved_status)));
        assert(gopine_ble_start_phone(time,80,0)==BLE_HS_EBUSY);
        assert(gopine_ble_start_weather(time,80,30000)==BLE_HS_EBUSY);
        assert(!memcmp(saved,&mode_payload,sizeof(saved)));
        assert(!gopine_ble_updates()); // In-flight bytes are not weather sizes.
        assert(!gopine_ble_take_weather(out) && !gopine_ble_take_music(out));
        for(unsigned i=0;i<2;i++)assert(phone_peers[i].handle==BLE_HS_CONN_HANDLE_NONE);
        put_le32(snapshot+8,236);gopine_ble_update_status(snapshot);gopine_ble_poll();
        gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    }
    // The deliberately disconnected unbonded peers leave diagnostic history;
    // reset it before the independent pairing-timeout fixture runs.
    assert(!gopine_ble_forget_phone());
    puts("100 Phone/weather/OTA payload transitions: busy starts, stale status, per-peer reset and wake isolation passed");
}

static void mode_payload_budget(void) {
    // These payload types contain only fixed-width scalar data, not pointers.
    // Measure alternative queue sizes without changing the active protocol.
    struct queue4 { struct update_packet packets[4]; uint8_t head,count; bool in_flight; };
    struct queue10 { struct update_packet packets[10]; uint8_t head,count; bool in_flight; };
    struct queue20 { struct update_packet packets[20]; uint8_t head,count; bool in_flight; };
    fprintf(stdout,"Payload budget: phone=%zu shared=%zu OTA2=%zu OTA4=%zu OTA10=%zu OTA20=%zu bytes\n",
           sizeof(mode_payload.phone),sizeof(mode_payload),sizeof(struct update_mailbox),
           sizeof(struct queue4),sizeof(struct queue10),sizeof(struct queue20));
    assert(sizeof(struct queue4)<=sizeof(mode_payload)+16);
}

static void ota_ignores_suspended_notification_wakeups(void) {
    const uint8_t address[]={3,5,7,9,11,0xce};
    uint8_t time[10]={0},out[ANCS_PACKET_SIZE];
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    connect_peer_on(1,address);mode_payload_quiet();
    // Start a client to create its session-clear record. Stop invalidates the
    // pending GATT callback, but preserves that clear until normal work resumes.
    gopine_ancs_poll(1,true);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    assert(gopine_ancs_updates());
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    for(unsigned i=0;i<8 && gopine_ble_host_work_pending();i++)gopine_ble_poll();
    gopine_ble_ack_updates();
    assert(gopine_ancs_updates() && !gopine_ble_updates());
    while(gopine_ble_take_apple_notification(out)){}
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    puts("OTA exclusive mode: suspended ANCS session-clear cannot busy-wake transfer loop");
}
