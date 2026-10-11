// InfiniLink 1.0.2 (d9f13a5) WelcomeView.swift gates Home on a connected
// peripheral AND nonempty DeviceInfo.firmware, populated only by 2A26.
static void firmware_read_with_snapshot(uint16_t handle,bool snapshot,uint8_t level) {
    uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,handle);
    att(read,sizeof(read),BLE_ATT_OP_READ_RSP);
    const char *firmware="goPine 0.3.27";
    unsigned response_size=9+strlen(firmware);
    assert(get_le16(reply+2)==response_size-4 && get_le16(reply+4)==response_size-8);
    assert(!memcmp(reply+9,firmware,strlen(firmware)));
    assert(reply_len==response_size+(snapshot?12:0));
    if(snapshot) {
        // An actual HCI/ATT response precedes the one-byte Battery notify.
        // A callback-only assertion would miss a redraw arriving too soon.
        const uint8_t *notification=reply+response_size;
        assert(get_le16(notification+2)==8 && get_le16(notification+4)==4);
        assert(get_le16(notification+6)==BLE_L2CAP_CID_ATT);
        assert(notification[8]==BLE_ATT_OP_NOTIFY_REQ);
        assert(get_le16(notification+9)==battery_handle && notification[11]==level);
    }
    reply_len=0;
    for(unsigned i=0;i<3;i++)gopine_ble_poll();
    assert(!reply_len); // No periodic refresh after the single snapshot.
}

static void phone_device_information_snapshot(uint16_t firmware_handle) {
    uint16_t cccd_handle;
    assert(!ble_gatts_find_dsc(BLE_UUID16_DECLARE(0x180f),BLE_UUID16_DECLARE(0x2a19),
                             BLE_UUID16_DECLARE(0x2902),&cccd_handle));
    uint8_t subscribe[]={BLE_ATT_OP_WRITE_REQ,0,0,1,0};put_le16(subscribe+1,cccd_handle);
    uint8_t unsubscribe[]={BLE_ATT_OP_WRITE_REQ,0,0,0,0};put_le16(unsubscribe+1,cccd_handle);
    // The snapshot is the current UI sample; subscribing alone must not send
    // it in response to the earlier unsubscribed Firmware Revision read.
    gopine_ble_update_battery(73);gopine_ble_poll();
    att(subscribe,sizeof(subscribe),BLE_ATT_OP_WRITE_RSP);assert(reply_len==9);
    reply_len=0;gopine_ble_poll();assert(!reply_len);
    uint8_t invalid_blob[]={BLE_ATT_OP_READ_BLOB_REQ,0,0,255,0};
    put_le16(invalid_blob+1,firmware_handle);
    att(invalid_blob,sizeof(invalid_blob),BLE_ATT_OP_ERROR_RSP);
    assert(reply_len==13 && reply[12]==BLE_ATT_ERR_INVALID_OFFSET);
    reply_len=0;gopine_ble_poll();assert(!reply_len);
    fprintf(stderr,"InfiniLink classic redraw: Firmware response before one subscribed Battery snapshot\n");
    firmware_read_with_snapshot(firmware_handle,true,73);
    firmware_read_with_snapshot(firmware_handle,false,73);
    att(unsubscribe,sizeof(unsubscribe),BLE_ATT_OP_WRITE_RSP);assert(reply_len==9);
    firmware_read_with_snapshot(firmware_handle,false,73);
    att(subscribe,sizeof(subscribe),BLE_ATT_OP_WRITE_RSP);assert(reply_len==9);
    firmware_read_with_snapshot(firmware_handle,false,73); // Once per link, not subscription.

    uint8_t disconnected[]={BLE_HCI_EVCODE_DISCONN_CMP,4,0,1,0,BLE_ERR_CONN_SPVN_TMO};
    send_event(disconnected,sizeof(disconnected));gopine_ble_poll();connect_peer();
    firmware_read_with_snapshot(firmware_handle,false,73); // Subscription was cleared.
    att(subscribe,sizeof(subscribe),BLE_ATT_OP_WRITE_RSP);assert(reply_len==9);
    firmware_read_with_snapshot(firmware_handle,true,73); // A fresh link gets one snapshot.
    firmware_read_with_snapshot(firmware_handle,false,73);
}

static void phone_device_information(void) {
    uint8_t time[10]={0};
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();connect_peer();
    assert(!gopine_sm_progress(1) && !phone_link_ready(1) && !gopine_bond_exists());
    bool classic_home_ready=false;
    uint8_t battery_read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(battery_read+1,battery_handle);
    att(battery_read,sizeof(battery_read),BLE_ATT_OP_READ_RSP);
    assert(reply_len==10 && reply[9]==80);
    assert(!classic_home_ready); // Battery receipt does not populate firmware.

    // Discover the service and its characteristics over ATT. A host lookup of
    // a known handle would miss the classic companion's registration failure.
    const uint8_t service_request[]={BLE_ATT_OP_FIND_TYPE_VALUE_REQ,1,0,255,255,0,0x28,0x0a,0x18};
    fprintf(stderr,"InfiniLink classic Home requires public DIS Firmware Revision\n");
    att(service_request,sizeof(service_request),BLE_ATT_OP_FIND_TYPE_VALUE_RSP);
    assert(reply_len==13);
    uint16_t start=get_le16(reply+9),end=get_le16(reply+11);
    uint16_t last_existing_handle;
    assert(!ble_gatts_find_chr(&music_uuids[0].u,&music_uuids[12].u,NULL,&last_existing_handle));
    assert(start>last_existing_handle); // DIS must follow the old database.
    uint8_t characteristics[]={BLE_ATT_OP_READ_TYPE_REQ,0,0,0,0,3,0x28};
    put_le16(characteristics+1,start);put_le16(characteristics+3,end);
    att(characteristics,sizeof(characteristics),BLE_ATT_OP_READ_TYPE_RSP);
    assert(reply[9]==7 && reply_len==31);
    uint16_t handles[3]={0};
    const uint16_t uuids[]={0x2a24,0x2a26,0x2a29};
    for(unsigned i=0;i<3;i++) {
        const uint8_t *entry=reply+10+i*7;
        assert(entry[2]==BLE_GATT_CHR_PROP_READ && get_le16(entry+5)==uuids[i]);
        handles[i]=get_le16(entry+3);
    }
    const char *values[]={"PineTime","goPine 0.3.27","PINE64"};
    assert(!classic_home_ready); // A connection and Battery alone cannot pass.
    for(unsigned i=0;i<3;i++) {
        uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,handles[i]);
        att(read,sizeof(read),BLE_ATT_OP_READ_RSP);
        unsigned size=reply_len-9;
        assert(size==strlen(values[i]) && !memcmp(reply+9,values[i],size));
        for(unsigned j=0;j<size;j++)assert(reply[9+j]>=0x20 && reply[9+j]<=0x7e);
        if(i==1)classic_home_ready=connection!=BLE_HS_CONN_HANDLE_NONE && size>0;
        uint8_t write[]={BLE_ATT_OP_WRITE_REQ,0,0,'x'};put_le16(write+1,handles[i]);
        att(write,sizeof(write),BLE_ATT_OP_ERROR_RSP);
        assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED);
    }
    assert(classic_home_ready && !gopine_sm_progress(1) && !phone_link_ready(1));
    write_characteristic(BLE_UUID16_DECLARE(0x1805),BLE_UUID16_DECLARE(0x2a2b),time,sizeof(time),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_AUTHEN);
    // Public metadata may not change midway through a connection.
    gopine_ble_set_firmware_version((const uint8_t*)"changed",7);
    assert(!strcmp(device_information.firmware,"goPine 0.3.27"));
    phone_device_information_snapshot(handles[1]);
    gopine_ble_stop();gopine_ble_poll();
    assert(!gopine_ble_busy());
    // Reconnect diagnostics deliberately survive stop/start; clear this
    // fixture's timeout before the later independent pairing-timeout test.
    assert(!gopine_ble_forget_phone());
}
