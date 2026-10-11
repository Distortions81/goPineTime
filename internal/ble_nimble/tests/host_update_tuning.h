// Run through the real pinned GAP/L2CAP procedure and HCI completion path.
static void ota_tuning_complete(uint16_t handle,uint16_t interval,uint16_t latency) {
    struct ble_hci_ev_le_subev_conn_upd_complete update={
        .subev_code=BLE_HCI_LE_SUBEV_CONN_UPD_COMPLETE,.conn_handle=handle,
        .conn_itvl=interval,.conn_latency=latency,.supervision_timeout=400
    };
    uint8_t event[sizeof(update)+2]={BLE_HCI_EVCODE_LE_META,sizeof(update)};
    memcpy(event+2,&update,sizeof(update));send_event(event,sizeof(event));gopine_ble_poll();
}
static void ota_connection_tuning(void) {
    const uint8_t addresses[3][6]={{1,2,3,4,5,0xc5},{1,2,3,4,5,0xc6},{1,2,3,4,5,0xc7}};
    uint16_t control,data,status;
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    for(unsigned scenario=0;scenario<6;scenario++) {
        unsigned before=parameter_requests;
        accept_parameter_request=scenario==1 || scenario==5;
        assert(!gopine_ble_start_update(80));gopine_ble_poll();
        for(unsigned i=0;i<3;i++)connect_peer_on(i+1,addresses[i]);
        ota_subscribe_on(1,status,true);ota_subscribe_on(3,status,true);
        assert(parameter_requests==before); // Nearby phones and subscriptions never tune.
        if(scenario==2)ota_tuning_complete(3,12,0); // Already fast: avoid needless update.
        if(scenario>=4) {
            // Exercise the HCI route too, using the real remote-feature event.
            struct ble_hci_ev_le_subev_rd_rem_used_feat features={
                .subev_code=BLE_HCI_LE_SUBEV_RD_REM_USED_FEAT,.conn_handle=3,
                .features={BLE_HS_HCI_LE_FEAT_CONN_PARAM_REQUEST}
            };
            uint8_t event[sizeof(features)+2]={BLE_HCI_EVCODE_LE_META,sizeof(features)};
            memcpy(event+2,&features,sizeof(features));send_event(event,sizeof(event));gopine_ble_poll();
        }
        if(scenario==3) {
            // An existing real GAP procedure causes EALREADY synchronously.
            hold_parameter_response=true;
            const struct ble_gap_upd_params existing={
                .itvl_min=24,.itvl_max=24,.latency=0,.supervision_timeout=400
            };
            assert(!ble_gap_update_params(3,&existing));assert(parameter_requests==before+1);
            before=parameter_requests;
        }
        uint8_t begin[]={1,42,0,0,0,236,0,0,0},out[244];unsigned size;
        ota_write_on(3,control,begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
        for(unsigned i=0;i<4;i++)gopine_ble_poll();
        assert(update_links.owner==3);
        assert(parameter_requests==before+(scenario<2 || scenario>=4));
        assert(update_links_find(&update_links,3)->tuning_attempted);
        if(scenario<2 || scenario>=4) {
            assert(parameter_handle==3 && parameter_values[0]==6 && parameter_values[1]==12 &&
                   !parameter_values[2] && parameter_values[3]==400);
        }
        struct ble_gap_conn_desc desc;
        for(unsigned handle=1;handle<=3;handle++) {
            assert(!ble_gap_conn_find(handle,&desc));
            assert(desc.conn_itvl==(handle==3 && (scenario==1 || scenario==2 || scenario==5)?12:24));
        }
        assert(!gopine_ble_error()); // Rejection and EALREADY never poison OTA.
        assert(gopine_ble_take_update(out,&size)==1 && size==sizeof(begin));
        uint8_t snapshot[16]={2,2};put_le32(snapshot+4,236);put_le32(snapshot+12,42);
        gopine_ble_update_status(snapshot);gopine_ble_poll();
        uint8_t chunk[]={42,0,0,0,0,0,0,0,0x59};
        ota_write_on(3,data,chunk,sizeof(chunk),BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_update(out,&size)==2 && size==sizeof(chunk));
        unsigned attempts=parameter_requests;
        for(unsigned i=0;i<20;i++)gopine_ble_poll();
        assert(parameter_requests==attempts);
        disconnect_peer_on(1);assert(update_links.owner==3 && parameter_requests==attempts);
        disconnect_peer_on(3);hold_parameter_response=false;
        // Reusing the same controller handle is a new link and may tune once.
        connect_peer_on(3,addresses[2]);ota_subscribe_on(3,status,true);
        ota_write_on(3,data,chunk,sizeof(chunk),BLE_ATT_OP_WRITE_RSP);
        for(unsigned i=0;i<4;i++)gopine_ble_poll();
        assert(parameter_requests==attempts+1 && parameter_handle==3 && !gopine_ble_error());
        gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    }
    accept_parameter_request=false;
    unsigned before=parameter_requests;
    uint8_t time[10]={0};assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    connect_peer_on(1,addresses[0]);connect_peer_on(2,addresses[1]);
    for(unsigned i=0;i<4;i++)gopine_ble_poll();
    assert(parameter_requests==before); // Ordinary feature links retain their parameters.
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    puts("OTA connection tuning: owner only, one request, rejection, busy fallback and handle reuse passed");
}
