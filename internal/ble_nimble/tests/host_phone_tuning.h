// Real saved-key restore, GAP/L2CAP requests and HCI completion. The simulated
// central accepts/rejects parameters; no service callbacks/security flags are
// injected to make the tuning policy run.
static void phone_connection_tuning(const struct ble_store_value_sec *first,
                                    const struct ble_store_value_sec *second) {
    uint8_t time[10]={0},out[ANCS_PACKET_SIZE];
    for(unsigned scenario=0;scenario<5;scenario++) {
        client_request_count=0;retain_apple=true;
        memset(apple_servers,0,sizeof(apple_servers));
        apple_servers[0].available=apple_servers[1].available=true;
        unsigned before=parameter_requests;
        accept_parameter_request=scenario!=0;
        assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
        connect_peer_on(1,first->peer_addr.val);
        request_encryption_on(1,first->ltk);apple_drain();
        assert(phone_link_ready(1));
        const struct phone_tuning wrapped={.at=24999};
        assert(phone_tuning_delay(&wrapped,1,UINT32_MAX-5000)==30000);
        assert(phone_tuning_delay(&wrapped,1,24999)==0);
        if(scenario==2) {
            struct ble_hci_ev_le_subev_conn_upd_complete update={
                .subev_code=BLE_HCI_LE_SUBEV_CONN_UPD_COMPLETE,.conn_handle=1,
                .conn_itvl=160,.supervision_timeout=600
            }; // Already economical, 200 ms.
            uint8_t event[sizeof(update)+2]={BLE_HCI_EVCODE_LE_META,sizeof(update)};
            memcpy(event+2,&update,sizeof(update));send_event(event,sizeof(event));gopine_ble_poll();
        }
        if(scenario==3) {
            struct ble_hci_ev_le_subev_rd_rem_used_feat features={
                .subev_code=BLE_HCI_LE_SUBEV_RD_REM_USED_FEAT,.conn_handle=1,
                .features={BLE_HS_HCI_LE_FEAT_CONN_PARAM_REQUEST}
            };
            uint8_t event[sizeof(features)+2]={BLE_HCI_EVCODE_LE_META,sizeof(features)};
            memcpy(event+2,&features,sizeof(features));send_event(event,sizeof(event));gopine_ble_poll();
        }
        test_rtc0.COUNTER+=29*32768;gopine_ble_poll();
        assert(parameter_requests==before && !phone_peer_find(1)->tuning.attempted);
        connect_peer_on(2,second->peer_addr.val);
        assert(!phone_link_ready(2));
        if(scenario==4) {
            hold_parameter_response=true;
            const struct ble_gap_upd_params existing={
                .itvl_min=24,.itvl_max=24,.latency=0,.supervision_timeout=400
            };
            assert(!ble_gap_update_params(1,&existing));
            before=parameter_requests;
        }
        test_rtc0.COUNTER+=32768;
        assert(phone_peers_delay(ble_npl_time_get())==0);
        assert(gopine_ble_updates() && gopine_ble_idle_ms()==0);
        for(unsigned i=0;i<4;i++)gopine_ble_poll();
        assert(phone_peer_find(1)->tuning.attempted && !phone_peer_find(2)->tuning.attempted);
        assert(parameter_requests==before+(scenario!=2 && scenario!=4));
        struct ble_gap_conn_desc desc;assert(!ble_gap_conn_find(1,&desc));
        if(scenario==1 || scenario==3) {
            assert(parameter_handle==1 && parameter_values[0]==24 && parameter_values[1]==36 &&
                   parameter_values[2]==4 && parameter_values[3]==600);
            assert(desc.conn_itvl==36 && desc.conn_latency==4 && desc.supervision_timeout==600);
        } else assert(desc.conn_itvl==(scenario==2?160:24) && !desc.conn_latency);
        assert(!gopine_ble_error() && phone_link_ready(1));
        unsigned attempts=parameter_requests;
        for(unsigned i=0;i<20;i++)gopine_ble_poll();
        assert(parameter_requests==attempts); // Rejection/busy never creates a retry loop.
        if(scenario==4) {
            // Complete the competing procedure before its genuine GAP timeout.
            hold_parameter_response=false;
            uint8_t rejected[]={BLE_L2CAP_SIG_OP_UPDATE_RSP,parameter_identifier,2,0,1,0};
            receive_l2cap_on(1,BLE_L2CAP_CID_SIG,rejected,sizeof(rejected));
        }
        // The other phone has its own grace period and security procedure.
        request_encryption_on(2,second->ltk);apple_drain();gopine_ble_poll();
        assert(phone_link_ready(2) && !phone_peer_find(2)->tuning.attempted);
        assert(parameter_requests==attempts);
        test_rtc0.COUNTER+=30*32768;gopine_ble_poll();gopine_ble_poll();
        assert(phone_peer_find(2)->tuning.attempted);
        assert(parameter_requests==attempts+1 && parameter_handle==2);
        assert(!gopine_ble_error());
        // Notification/music feature writes remain usable after negotiation.
        assert(phone_link_ready(1) && phone_link_ready(2));
        write_characteristic_on(1,&music_uuids[0].u,&music_uuids[4].u,
                                companion_track,sizeof(companion_track),BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_music(out));
        assert(apple_servers[0].source_subscribed && apple_servers[1].source_subscribed);
        apple_connection=1;apple_source(0x12345678,1);apple_drain();
        bool received=false;
        while(gopine_ble_take_apple_notification(out))if(out[4]==1)received=true;
        assert(received);
        disconnect_peer_on(1);hold_parameter_response=false;
        connect_peer_on(1,first->peer_addr.val);request_encryption_on(1,first->ltk);apple_drain();
        assert(!phone_peer_find(1)->tuning.attempted);
        attempts=parameter_requests;
        test_rtc0.COUNTER+=30*32768;gopine_ble_poll();gopine_ble_poll();
        assert(parameter_requests==attempts+1 && parameter_handle==1);
        gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    }
    accept_parameter_request=false;
    retain_apple=false;apple_connection=1;
    // An unpaired visitor has no automatic security procedure to time out.
    // Passing the grace deadline must neither tune it nor leave a due timer.
    unsigned before=parameter_requests;
    const uint8_t unpaired[]={2,7,1,8,2,0xcd};
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();connect_peer_on(1,unpaired);
    test_rtc0.COUNTER+=31*32768;gopine_ble_poll();
    assert(phone_peer_find(1) && !phone_peer_find(1)->tuning.attempted);
    assert(phone_tuning_delay(&phone_peer_find(1)->tuning,1,ble_npl_time_get())==UINT32_MAX);
    assert(parameter_requests==before);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    puts("phone power tuning: grace, authentication, two peers, L2CAP/HCI, rejection, busy, existing timing and handle reuse passed");
}
