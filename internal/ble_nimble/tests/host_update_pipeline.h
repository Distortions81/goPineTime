// Exercise Write Commands through the pinned ATT host, including real 27-byte
// HCI fragmentation. Flash completion is supplied separately through the Go
// bridge; accepting a packet must never publish a durable acknowledgement.
static void pipeline_command(uint16_t connection,uint16_t data,uint32_t session,
                             uint32_t offset,unsigned payload) {
    uint8_t packet[247]={BLE_ATT_OP_WRITE_CMD};assert(payload && payload<=236);
    put_le16(packet+1,data);put_le32(packet+3,session);put_le32(packet+7,offset);
    for(unsigned i=0;i<payload;i++)packet[11+i]=(uint8_t)(offset+i);
    receive_l2cap_on(connection,BLE_L2CAP_CID_ATT,packet,11+payload);
    assert(!reply_len); // Write Commands have neither response nor error PDUs.
}
static void pipeline_publish(uint32_t total,uint32_t offset) {
    uint8_t status[16]={2,2,0,0};put_le32(status+4,total);
    put_le32(status+8,offset);put_le32(status+12,42);
    gopine_ble_update_status(status);
}
static void pipeline_take(uint32_t offset,unsigned payload) {
    uint8_t packet[244];unsigned size;
    assert(gopine_ble_take_update(packet,&size)==2 && size==payload+8);
    assert(get_le32(packet)==42 && get_le32(packet+4)==offset);
    for(unsigned i=0;i<payload;i++)assert(packet[8+i]==(uint8_t)(offset+i));
}
static void ota_pipeline(void) {
    const uint8_t owner[]={6,5,4,3,2,0xc8},spectator[]={6,5,4,3,2,0xc9};
    uint16_t control,data,status;
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    capture_connections=true;connect_peer_on(1,owner);connect_peer_on(2,spectator);
    ota_mtu_on(1,247);ota_subscribe_on(1,status,true);ota_subscribe_on(2,status,true);
    uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,control);
    att_on(1,read,sizeof(read),BLE_ATT_OP_READ_RSP);
    const uint8_t capabilities[]={1,2,1,0};
    assert(reply_len==13 && !memcmp(reply+9,capabilities,4));
    put_le16(read+1,data-1);att_on(1,read,sizeof(read),BLE_ATT_OP_READ_RSP);
    assert((reply[9]&(BLE_GATT_CHR_PROP_WRITE|BLE_GATT_CHR_PROP_WRITE_NO_RSP))==
           (BLE_GATT_CHR_PROP_WRITE|BLE_GATT_CHR_PROP_WRITE_NO_RSP));
    // Repeated wraparound runs above the small mbuf/ACL pool capacities.
    const uint32_t total=236*20+19;
    uint8_t begin[]={1,42,0,0,0,0,0,0,0},out[244];unsigned size;
    put_le32(begin+5,total);ota_write_on(1,control,begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_update(out,&size)==1);pipeline_publish(total,0);gopine_ble_poll();
    for(uint32_t offset=0;offset<236*20;offset+=472) {
        pipeline_command(1,data,42,offset,236);
        pipeline_command(1,data,42,offset+236,236);
        assert(update_incoming.count==2 && get_le32(update_status+8)==offset);
        struct update_mailbox expected=update_incoming;
        pipeline_command(1,data,42,offset+472,1);ota_assert_mailbox(&expected);
        pipeline_command(2,data,42,offset,1);ota_assert_mailbox(&expected);
        uint8_t verify[]={2,42,0,0,0};
        ota_write_on(1,control,verify,sizeof(verify),BLE_ATT_OP_ERROR_RSP);
        ota_assert_mailbox(&expected);
        pipeline_take(offset,236);
        expected=update_incoming;
        pipeline_command(1,data,42,offset+472,1);ota_assert_mailbox(&expected);
        assert(!gopine_ble_take_update(out,&size)); // In-flight slot still reserved.
        pipeline_publish(total,offset+236);pipeline_take(offset+236,236);
        pipeline_publish(total,offset+472);ota_clear_replies();gopine_ble_poll();
        assert(ota_notifications(1,status,244)==1 && !update_incoming.count);
        assert(get_le32(reply+19)==offset+472); // Two commits, one cumulative notification.
    }
    const uint32_t durable=236*20;
    // Invalid Write Commands are silent and cannot poison the next valid one.
    struct update_mailbox empty=update_incoming;
    pipeline_command(1,data,43,durable,1);ota_assert_mailbox(&empty);
    pipeline_command(1,data,42,durable+1,1);ota_assert_mailbox(&empty);
    pipeline_command(1,data,42,durable-1,1);ota_assert_mailbox(&empty);
    pipeline_command(1,data,42,durable,19);assert(update_incoming.count==1);
    disconnect_peer_on(1);assert(!update_incoming.count);
    assert(get_le32(update_status+8)==durable); // Queued bytes were never committed.
    struct ble_gap_conn_desc desc;assert(!ble_gap_conn_find(2,&desc));
    connect_peer_on(3,owner);ota_subscribe_on(3,status,true); // Reconnect at default MTU23.
    ota_status_on(3,status,20);assert(get_le32(reply+17)==durable);
    pipeline_command(3,data,42,durable,12);pipeline_command(3,data,42,durable+12,7);
    pipeline_take(durable,12);pipeline_publish(total,durable+12);
    pipeline_take(durable+12,7);pipeline_publish(total,total);gopine_ble_poll();
    uint8_t verify[]={2,42,0,0,0};
    ota_write_on(3,control,verify,sizeof(verify),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_update(out,&size)==1 && size==5 && out[0]==2);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();assert(!gopine_ble_busy());
    capture_connections=false;
    puts("OTA pipeline: capability discovery, two durable slots, WNR pressure, cumulative ACK, spectator isolation and MTU-changing resume passed");
}
