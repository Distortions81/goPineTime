// Exercise a saturated trace response at the central's large negotiated MTU.
// Real radio RX mbufs carry the controller header and two bytes of remaining
// leading space after the LL prepends HCI. Retain transmitted fragments until
// a later connection event, so the one-element fragment pool is not instantly
// reused for every fragment as it is in the simplest fake controller.
static void phone_trace_large_mtu(void) {
    assert(!gopine_ble_start_update(80));gopine_ble_poll();connect_peer();
    uint8_t mtu[]={BLE_ATT_OP_MTU_REQ,247,0};att(mtu,sizeof(mtu),BLE_ATT_OP_MTU_RSP);
    uint16_t handle;
    assert(!ble_gatts_find_chr(&pairing_trace_service.u,&pairing_trace_characteristic.u,NULL,&handle));
    while(trace_count<24)trace_pairing(13,0x1007);
    uint8_t expected[196];memcpy(expected,pairing_trace,sizeof(expected));
    // Current bond metadata is refreshed by the read callback itself.
    expected[3]=(uint8_t)trace_bond_flags(BLE_ADDR_ANY);
    // Reserve up to three msys buffers for unrelated queued radio work.
    for(unsigned pressure=0;pressure<=3;pressure++) {
        struct os_mbuf *held[3];
        for(unsigned j=0;j<pressure;j++){held[j]=os_msys_get(1,0);assert(held[j]);}
        struct os_mbuf *om=os_msys_get_pkthdr(11,sizeof(struct ble_mbuf_hdr));assert(om);
        om->om_data+=2;
        uint8_t request[]={1,0x20,7,0,3,0,4,0,BLE_ATT_OP_READ_REQ,0,0};
        put_le16(request+9,handle);assert(!os_mbuf_append(om,request,sizeof(request)));
        reply_len=0;defer_acl=true;
        assert(!ble_hci_trans_ll_acl_tx(om));gopine_ble_poll();
        for(unsigned j=0;j<pressure;j++)os_mbuf_free_chain(held[j]);
        assert(pending_acl_count==4);
        defer_acl=false;
        for(unsigned i=0;i<pending_acl_count;i++)assert(!ble_ll_hci_acl_rx(pending_acl[i],NULL));
        pending_acl_count=0;gopine_ble_poll();
        assert(reply_len==205 && get_le16(reply+4)==197 && reply[8]==BLE_ATT_OP_READ_RSP);
        assert(!memcmp(reply+9,expected,sizeof(expected)));
    }
    // The short OTA status read should reuse the incoming mbuf even if every
    // other msys block is occupied. It requires no response fragmentation.
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&handle));
    struct os_mbuf *short_read=os_msys_get_pkthdr(11,sizeof(struct ble_mbuf_hdr));assert(short_read);
    short_read->om_data+=2;
    uint8_t status_request[]={1,0x20,7,0,3,0,4,0,BLE_ATT_OP_READ_REQ,0,0};
    put_le16(status_request+9,handle);
    assert(!os_mbuf_append(short_read,status_request,sizeof(status_request)));
    struct os_mbuf *held[MYNEWT_VAL(MSYS_1_BLOCK_COUNT)];unsigned held_count=0;
    while(os_msys_num_free()) {held[held_count]=os_msys_get(1,0);assert(held[held_count]);held_count++;}
    reply_len=0;defer_acl=true;
    assert(!ble_hci_trans_ll_acl_tx(short_read));gopine_ble_poll();
    assert(pending_acl_count==1 && !os_msys_num_free());
    for(unsigned i=0;i<held_count;i++)os_mbuf_free_chain(held[i]);
    defer_acl=false;assert(!ble_ll_hci_acl_rx(pending_acl[0],NULL));
    pending_acl_count=0;gopine_ble_poll();
    assert(reply_len==25 && get_le16(reply+4)==17 && reply[8]==BLE_ATT_OP_READ_RSP);
    assert(!memcmp(reply+9,update_status,3) && reply[12]==244 &&
           !memcmp(reply+13,update_status+4,12));
    gopine_ble_stop();gopine_ble_poll();assert(!gopine_ble_busy());
}
