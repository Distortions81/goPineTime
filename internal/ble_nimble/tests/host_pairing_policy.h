// Real ATT/HCI peer requests must fail before an unusable association generates
// public keys or occupies the watch's only saved-phone slot.
static void assert_last_security_trace(uint8_t event,uint16_t status) {
    assert(trace_count);
    const uint8_t *last=pairing_trace+4+(trace_count-1)*8;
    assert(last[4]==event && get_le16(last+6)==status);
}
static void phone_pairing_policy(void) {
    uint8_t time[10]={0};
    assert(!gopine_ble_forget_phone());
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();connect_peer();
    unsigned saved_writes=flash_writes,crypto_before=crypto_yields;
    const struct {
        uint8_t io,oob,auth,key_size,error;
    } cases[]={
        {BLE_SM_IO_CAP_NO_IO,0,0x0d,16,BLE_SM_ERR_AUTHREQ},
        {BLE_SM_IO_CAP_DISP_ONLY,0,0x0d,16,BLE_SM_ERR_AUTHREQ},
        {BLE_SM_IO_CAP_DISP_YES_NO,0,0x0d,16,BLE_SM_ERR_AUTHREQ},
        {BLE_SM_IO_CAP_RESERVED,0,0x0d,16,BLE_SM_ERR_AUTHREQ},
        {BLE_SM_IO_CAP_KEYBOARD_DISP,1,0x0d,16,BLE_SM_ERR_AUTHREQ},
        {BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,7,BLE_SM_ERR_ENC_KEY_SZ},
        {BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,12,BLE_SM_ERR_ENC_KEY_SZ},
        {BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,15,BLE_SM_ERR_ENC_KEY_SZ},
        {BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0c,16,BLE_SM_ERR_AUTHREQ},
        // Preserve upstream malformed-key-size and unsupported-legacy errors.
        {BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,17,BLE_SM_ERR_INVAL},
        {BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x05,16,BLE_SM_ERR_AUTHREQ},
    };
    for(unsigned i=0;i<sizeof(cases)/sizeof(cases[0]);i++) {
        uint8_t request[]={BLE_SM_OP_PAIR_REQ,cases[i].io,cases[i].oob,
                           cases[i].auth,cases[i].key_size,3,3};
        receive_l2cap(BLE_L2CAP_CID_SM,request,sizeof(request));
        assert(reply_len==10 && get_le16(reply+6)==BLE_L2CAP_CID_SM);
        assert(reply[8]==BLE_SM_OP_PAIR_FAIL && reply[9]==cases[i].error);
        assert(!gopine_sm_progress(1) && !pairing_code && !pairing_io_pending);
        assert(!gopine_bond_exists() && gopine_bond_status()==0);
        assert(flash_writes==saved_writes && crypto_yields==crypto_before);
        if(i<sizeof(cases)/sizeof(cases[0])-2)
            assert((uint16_t)gopine_ble_pairing_diagnostics()==BLE_HS_SM_US_ERR(cases[i].error));
        else
            assert_last_security_trace(17,BLE_HS_SM_US_ERR(cases[i].error));
        assert(connection==1);
    }
    // A central retaining an old bond requests its LTK without sending a new
    // Pair Request. The real host must nack an absent key without saving one
    // or leaving a procedure behind. Upstream has no GAP callback for this;
    // the explicit lookup trace is the only evidence beyond the disconnect.
    struct ble_hci_ev_le_subev_lt_key_req lookup={
        .subev_code=BLE_HCI_LE_SUBEV_LT_KEY_REQ,.conn_handle=1};
    uint8_t hci[sizeof(lookup)+2]={BLE_HCI_EVCODE_LE_META,sizeof(lookup)};
    memcpy(hci+2,&lookup,sizeof(lookup));
    unsigned negatives=ltk_negative_replies,positives=ltk_replies;
    uint32_t diagnostics=gopine_ble_pairing_diagnostics();
    send_event(hci,sizeof(hci));gopine_ble_poll();
    assert(ltk_negative_replies==negatives+1 && ltk_replies==positives);
    assert(!gopine_sm_progress(1) && !pairing_code && !pairing_io_pending);
    assert(!gopine_bond_exists() && flash_writes==saved_writes);
    assert(gopine_ble_pairing_diagnostics()==diagnostics);
    assert_last_security_trace(16,BLE_HS_ENOENT);
    assert(connection==1);
    // Normal keyboard/display pairing works immediately on the same link;
    // rejection or a missing LTK must not leave an SM procedure blocking it.
    uint8_t request[]={BLE_SM_OP_PAIR_REQ,BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,16,3,3};
    receive_l2cap(BLE_L2CAP_CID_SM,request,sizeof(request));
    assert(reply_len==15 && reply[8]==BLE_SM_OP_PAIR_RSP);
    uint8_t private[32]={0},public[64],packet[65]={BLE_SM_OP_PAIR_PUBLIC_KEY};
    private[31]=3;
    assert(uECC_compute_public_key(private,public,uECC_secp256r1())==1);
    for(unsigned i=0;i<32;i++) {packet[1+i]=public[31-i];packet[33+i]=public[63-i];}
    receive_l2cap(BLE_L2CAP_CID_SM,packet,sizeof(packet));
    assert(pairing_code>0 && pairing_code<=1000000 && !pairing_io_pending);
    assert(gopine_sm_progress(1)==0x200 && !gopine_bond_exists());
    gopine_ble_stop();gopine_ble_poll();
    assert(!gopine_ble_busy() && !pairing_code && !gopine_bond_exists());
    // A PC may request a missing LTK in OTA mode too. Do not let diagnostic
    // collection itself overwrite the retained Phone trace, or accept unknown
    // event codes through the narrow tracing callback.
    assert(!gopine_ble_start_update(80));gopine_ble_poll();connect_peer();
    uint8_t retained[sizeof(pairing_trace)];memcpy(retained,pairing_trace,sizeof(retained));
    negatives=ltk_negative_replies;
    send_event(hci,sizeof(hci));gopine_ble_poll();
    assert(ltk_negative_replies==negatives+1 && !gopine_sm_progress(1));
    gopine_ble_trace_security(16,BLE_HS_ENOENT);
    gopine_ble_trace_security(17,BLE_HS_EINVAL);
    gopine_ble_trace_security(255,0);
    assert(!memcmp(retained,pairing_trace,sizeof(retained)));
    gopine_ble_stop();gopine_ble_poll();assert(!gopine_ble_busy());
}
