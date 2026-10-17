// Real pinned host + production service; controller capabilities, HCI, hardware
// and flash are simulated. No physical radio timing or Go stack is exercised.
#include <assert.h>
#include <stdio.h>
#include "../port/service.c"
#include "ble_hs_priv.h"
#include "ble_sm_priv.h"
#include "../port/bond_journal.h"
#include "transport/ram/ble_hci_ram.h"
#include "tinycrypt/aes.h"
#include "tinycrypt/ecc.h"
#include "tinycrypt/ecc_dh.h"
#include "companion_fixtures.h"
extern size_t gopine_ble_alloc_peak(void);
extern size_t gopine_ble_alloc_capacity(void);

test_rtc test_rtc0;
test_scb test_scb0;
test_ficr test_ficr0 = {{0x157a69b5, 0xc99e}};
uint32_t test_primask;
static struct ble_npl_eventq test_hostq;
static unsigned commands;
static uint16_t advertised_min,advertised_max;
static uint8_t reply[512];
static unsigned reply_len;
static uint32_t flash[BOND_PAGE_SIZE/4];
static uint8_t controller_ltk[16];
static unsigned ltk_replies, ltk_negative_replies;
static bool defer_acl, burst_rx;
static struct os_mbuf *pending_acl[4];
static unsigned pending_acl_count;
static uint8_t peer_address[6]={1,2,3,4,5,0xc0};
static bool controller_resolving;
static struct ble_hci_le_add_resolv_list_cp resolving_entries[MYNEWT_VAL(BLE_LL_RESOLV_LIST_SIZE)];
static unsigned resolving_count, controller_resets;
static unsigned flash_writes;
static bool reject_peer_irk;
static bool reject_peer_irk_remove;
static unsigned parameter_requests;
// Seed retained PHY state at init/reset so every shutdown exercises cleanup;
// real init/reset preserve it rather than enabling it unconditionally.
// Every final crystal release must follow explicit CCM/AAR shutdown.
static bool crypto_peripheral_enabled;
static void assert_stopped_idle(void);
static uint16_t parameter_handle,parameter_values[4];
static bool accept_parameter_request,hold_parameter_response;
// Capture real outgoing client requests separately from the watch's server
// responses. A fake iOS GATT server answers these without calling ANCS callbacks.
struct client_request { uint8_t data[247]; unsigned size; uint16_t connection; };
static struct client_request client_requests[8];
static unsigned client_request_count, indication_confirmations;
// Reassemble each ACL stream independently: two real connections can interleave
// fragments, and completion credits belong to the handle that transmitted them.
struct captured_pdu { uint8_t data[512]; unsigned size; uint16_t connection; };
static struct captured_pdu assemblies[MYNEWT_VAL(BLE_MAX_CONNECTIONS)];
static struct captured_pdu captured[16];
static unsigned captured_count;
static bool capture_connections;
static bool retain_peer_data;
static bool retain_apple;
static struct ble_npl_eventq crypto_controller;
static struct ble_npl_event crypto_event, crypto_host_event;
static unsigned crypto_yields, crypto_controller_runs;
static bool in_crypto;
static void crypto_controller_callback(struct ble_npl_event *ev) {
    (void)ev;assert(in_crypto);crypto_controller_runs++;
}
static void crypto_host_callback(struct ble_npl_event *ev) {
    (void)ev;assert(!in_crypto);
}
void gopine_test_ecc_yield(void) {
    in_crypto=true;crypto_yields++;
    // Model an IRQ queuing link-layer work during scalar multiplication,
    // together with host data that must remain deferred until SM unwinds.
    ble_npl_eventq_put(&crypto_controller,&crypto_event);
    ble_npl_eventq_put(&test_hostq,&crypto_host_event);
    gopine_ble_controller_pump();
    assert(crypto_controller_runs==crypto_yields);
    assert(ble_npl_event_is_queued(&crypto_host_event));
    in_crypto=false;
}

void nimble_port_init(void) {
    extern void os_msys_init(void);
    ble_npl_eventq_init(&test_hostq);
    os_msys_init(); ble_hs_init(); ble_hci_ram_init();
}
struct ble_npl_eventq *nimble_port_get_dflt_eventq(void) { return &test_hostq; }
int ble_phy_init(void) { crypto_peripheral_enabled=true;return 0; }
int ble_phy_txpwr_set(int dbm) { (void)dbm; return 0; }
void ble_phy_disable(void) {}
void ble_phy_encrypt_disable(void) {crypto_peripheral_enabled=false;}
void ble_phy_rfclk_disable(void) {assert(!crypto_peripheral_enabled);}
int ble_hw_rng_stop(void) { return 0; }
int ble_hw_get_static_addr(ble_addr_t *addr) {
    addr->type=BLE_ADDR_RANDOM;memset(addr->val,0xc0,sizeof(addr->val));return 0;
}
int ble_ll_reset(void) { crypto_peripheral_enabled=true;controller_resolving=false;resolving_count=0;return 0; }
int ble_ll_rand_data_get(uint8_t *buf,uint8_t len) { memset(buf,42,len); return 0; }
uint32_t bond_flash_word(unsigned offset) {
    assert(offset<BOND_PAGE_SIZE && offset%4==0);return flash[offset/4];
}
bool bond_flash_write(unsigned offset,uint32_t word) {
    assert(offset<BOND_PAGE_SIZE && offset%4==0);
    assert((flash[offset/4]&word)==word);flash[offset/4]=word;flash_writes++;return true;
}
bool bond_flash_erase(void) { memset(flash,0xff,sizeof(flash));return true; }
int ble_hw_encrypt_block(struct ble_encryption_block *block) {
    struct tc_aes_key_sched_struct keys;
    return tc_aes128_set_encrypt_key(&keys,block->key) &&
           tc_aes_encrypt(block->cipher_text,block->plain_text,&keys) ? 0 : -1;
}
static void send_event(const void *data,unsigned size) {
    uint8_t *buf=ble_hci_trans_buf_alloc(BLE_HCI_TRANS_BUF_EVT_HI);
    assert(buf); memcpy(buf,data,size); assert(ble_hci_trans_ll_evt_tx(buf)==0);
}
void ble_ll_hci_send_noop(void) {
    uint8_t evt[]={BLE_HCI_EVCODE_COMMAND_COMPLETE,3,1,0,0};send_event(evt,sizeof(evt));
}
int ble_ll_hci_cmd_rx(uint8_t *cmd,void *arg) {
    (void)arg;commands++;
    uint16_t opcode=get_le16(cmd);
    uint8_t evt[96]={BLE_HCI_EVCODE_COMMAND_COMPLETE,4,1,0,0,0};
    put_le16(evt+3,opcode);
    uint16_t handle=get_le16(cmd+3);
    switch(opcode) {
    case BLE_HCI_OP(BLE_HCI_OGF_CTLR_BASEBAND,BLE_HCI_OCF_CB_RESET):
        controller_resolving=false;resolving_count=0;controller_resets++;
        break;
    case BLE_HCI_OP(BLE_HCI_OGF_INFO_PARAMS,BLE_HCI_OCF_IP_RD_LOCAL_VER):
        evt[1]+=8;evt[6]=BLE_HCI_VER_BCS_5_0;break;
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_RD_BUF_SIZE):
        evt[1]+=3;put_le16(evt+6,64);evt[8]=4;break;
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_RD_LOC_SUPP_FEAT):
        evt[1]+=8;evt[6]=0x41; // Encryption and controller privacy.
        break;
    case BLE_HCI_OP(BLE_HCI_OGF_INFO_PARAMS,BLE_HCI_OCF_IP_RD_BD_ADDR): {
        const uint8_t address[6]={0xb5,0x69,0x7a,0x15,0x9e,0xc9};
        evt[1]+=6;memcpy(evt+6,address,6);break;
    }
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_SET_ADV_PARAMS):
        advertised_min=get_le16(cmd+3);advertised_max=get_le16(cmd+5);break;
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_CONN_UPDATE):
        parameter_requests++;parameter_handle=handle;
        for(unsigned i=0;i<4;i++)parameter_values[i]=get_le16(cmd+5+2*i);
        if(!accept_parameter_request)evt[5]=BLE_ERR_CMD_DISALLOWED;
        break;
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_SET_ADDR_RES_EN):
        controller_resolving=cmd[3]!=0;break;
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_CLR_RESOLV_LIST):
        resolving_count=0;break;
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_RMV_RESOLV_LIST): {
        const struct ble_hci_le_rmv_resolve_list_cp *entry=(const void*)(cmd+3);
        if(reject_peer_irk_remove) {evt[5]=BLE_ERR_CMD_DISALLOWED;break;}
        unsigned at=0;
        for(;at<resolving_count;at++)
            if(entry->peer_addr_type==resolving_entries[at].peer_addr_type &&
               !memcmp(entry->peer_id_addr,resolving_entries[at].peer_id_addr,6))break;
        if(at==resolving_count) {evt[5]=BLE_ERR_UNK_CONN_ID;break;}
        memmove(resolving_entries+at,resolving_entries+at+1,
                (--resolving_count-at)*sizeof(resolving_entries[0]));
        break;
    }
    case BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_ADD_RESOLV_LIST): {
        struct ble_hci_le_add_resolv_list_cp entry;memcpy(&entry,cmd+3,sizeof(entry));
        if(reject_peer_irk && entry.peer_id_addr[5]==0xc0) {
            evt[5]=BLE_ERR_MEM_CAPACITY;break;
        }
        if(resolving_count==ARRAY_SIZE(resolving_entries)) {evt[5]=BLE_ERR_MEM_CAPACITY;break;}
        unsigned at=0;
        for(;at<resolving_count;at++)
            if(entry.peer_addr_type==resolving_entries[at].peer_addr_type &&
               !memcmp(entry.peer_id_addr,resolving_entries[at].peer_id_addr,6))break;
        // Match the pinned controller: insertion never silently replaces an
        // existing identity. Host startup already replays saved peer IRKs.
        if(at<resolving_count) {evt[5]=BLE_ERR_INV_HCI_CMD_PARMS;break;}
        resolving_entries[resolving_count++]=entry;
        break;
    }
    }
    if(opcode==BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_LT_KEY_REQ_REPLY)) {
        memcpy(controller_ltk,cmd+5,16);ltk_replies++;
        evt[1]+=2;put_le16(evt+6,handle);
    }
    if(opcode==BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_LT_KEY_REQ_NEG_REPLY)) {
        ltk_negative_replies++;
        evt[1]+=2;put_le16(evt+6,handle);
    }
    if(opcode==BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_RAND)) {
        evt[1]+=8;memset(evt+6,42,8); // Deterministic controller entropy in tests.
    }
    ble_hci_trans_buf_free(cmd);send_event(evt,evt[1]+2);
    if(opcode==BLE_HCI_OP(BLE_HCI_OGF_LINK_CTRL,BLE_HCI_OCF_DISCONNECT_CMD)) {
        uint8_t disconnected[]={BLE_HCI_EVCODE_DISCONN_CMP,4,0,0,0,BLE_ERR_REM_USER_CONN_TERM};
        put_le16(disconnected+3,handle);send_event(disconnected,sizeof(disconnected));
    }
    if(opcode==BLE_HCI_OP(BLE_HCI_OGF_LE,BLE_HCI_OCF_LE_CONN_UPDATE) && accept_parameter_request) {
        struct ble_hci_ev_le_subev_conn_upd_complete update={
            .subev_code=BLE_HCI_LE_SUBEV_CONN_UPD_COMPLETE,.conn_handle=handle,
            .conn_itvl=parameter_values[1],.conn_latency=parameter_values[2],
            .supervision_timeout=parameter_values[3]
        };
        uint8_t event[sizeof(update)+2]={BLE_HCI_EVCODE_LE_META,sizeof(update)};
        memcpy(event+2,&update,sizeof(update));send_event(event,sizeof(event));
    }
    return 0;
}
static bool capture_parameter_request(const struct captured_pdu *pdu) {
    if(pdu->size!=20 || get_le16(pdu->data+6)!=BLE_L2CAP_CID_SIG ||
       pdu->data[8]!=BLE_L2CAP_SIG_OP_UPDATE_REQ)return false;
    // The real host chooses L2CAP when remote LL parameter-request support
    // has not been reported. Model the central's reply through HCI, never by
    // calling the GAP callback or mutating the host connection directly.
    parameter_requests++;parameter_handle=pdu->connection;
    for(unsigned i=0;i<4;i++)parameter_values[i]=get_le16(pdu->data+12+2*i);
    if(hold_parameter_response)return true;
    uint8_t response[14]={0};
    put_le16(response,pdu->connection|0x2000);put_le16(response+2,10);
    put_le16(response+4,6);put_le16(response+6,BLE_L2CAP_CID_SIG);
    response[8]=BLE_L2CAP_SIG_OP_UPDATE_RSP;response[9]=pdu->data[9];
    put_le16(response+10,2);put_le16(response+12,accept_parameter_request?0:1);
    struct os_mbuf *incoming=os_msys_get_pkthdr(sizeof(response),0);assert(incoming);
    assert(!os_mbuf_append(incoming,response,sizeof(response)));
    assert(!ble_hci_trans_ll_acl_tx(incoming));
    if(accept_parameter_request) {
        struct ble_hci_ev_le_subev_conn_upd_complete update={
            .subev_code=BLE_HCI_LE_SUBEV_CONN_UPD_COMPLETE,
            .conn_handle=pdu->connection,.conn_itvl=parameter_values[1],
            .conn_latency=parameter_values[2],.supervision_timeout=parameter_values[3]
        };
        uint8_t event[sizeof(update)+2]={BLE_HCI_EVCODE_LE_META,sizeof(update)};
        memcpy(event+2,&update,sizeof(update));send_event(event,sizeof(event));
    }
    return true;
}
int ble_ll_hci_acl_rx(struct os_mbuf *om,void *arg) {
    (void)arg;
    if(defer_acl) {
        assert(pending_acl_count<ARRAY_SIZE(pending_acl));
        pending_acl[pending_acl_count++]=om;
        return 0;
    }
    unsigned size=OS_MBUF_PKTLEN(om);
    uint8_t header[4];assert(os_mbuf_copydata(om,0,4,header)==0);
    uint16_t handle=get_le16(header)&0x0fff;
    unsigned skip=(get_le16(header)&0x3000)==0x1000?4:0;
    struct captured_pdu *pdu=NULL;
    for(unsigned i=0;i<ARRAY_SIZE(assemblies);i++)
        if(assemblies[i].size && assemblies[i].connection==handle)pdu=&assemblies[i];
    if(!skip) {
        assert(!pdu);
        for(unsigned i=0;i<ARRAY_SIZE(assemblies);i++)
            if(!assemblies[i].size){pdu=&assemblies[i];pdu->connection=handle;break;}
    }
    assert(pdu && pdu->size+size-skip<=sizeof(pdu->data));
    assert(!os_mbuf_copydata(om,skip,size-skip,pdu->data+pdu->size));pdu->size+=size-skip;
    if(pdu->size>=8 && pdu->size==8u+get_le16(pdu->data+4)) {
        if(!capture_parameter_request(pdu)) {
        assert(reply_len+pdu->size<=sizeof(reply));
        memcpy(reply+reply_len,pdu->data,pdu->size);reply_len+=pdu->size;
        if(capture_connections) {
            assert(captured_count<ARRAY_SIZE(captured));captured[captured_count++]=*pdu;
        }
        if(get_le16(pdu->data+6)==BLE_L2CAP_CID_ATT) {
            const uint8_t *att=pdu->data+8;unsigned n=get_le16(pdu->data+4);
            if(att[0]==BLE_ATT_OP_FIND_TYPE_VALUE_REQ || att[0]==BLE_ATT_OP_READ_TYPE_REQ ||
               att[0]==BLE_ATT_OP_FIND_INFO_REQ || att[0]==BLE_ATT_OP_WRITE_REQ) {
                assert(client_request_count<ARRAY_SIZE(client_requests) && n<=247);
                struct client_request *request=&client_requests[client_request_count++];
                memcpy(request->data,att,n);request->size=n;request->connection=handle;
            } else if(att[0]==BLE_ATT_OP_INDICATE_RSP)indication_confirmations++;
        }
        }
        pdu->size=0;
    }
    os_mbuf_free_chain(om);
    uint8_t completed[]={BLE_HCI_EVCODE_NUM_COMP_PKTS,5,1,1,0,1,0};
    put_le16(completed+3,handle);
    send_event(completed,sizeof(completed));return 0;
}
static void connect_peer_on(uint16_t handle,const uint8_t address[6]) {
    // A synthetic connection must be possible on the actual host. In
    // particular, never bypass missing OTA advertising with a GAP shortcut.
    assert(ble_gap_adv_active());
    struct ble_hci_ev_le_subev_enh_conn_complete evt={.subev_code=BLE_HCI_LE_SUBEV_ENH_CONN_COMPLETE,
        .conn_handle=handle,.role=BLE_HCI_LE_CONN_COMPLETE_ROLE_SLAVE,
        .peer_addr_type=BLE_ADDR_RANDOM,.peer_addr={1,2,3,4,5,0xc0},
        .conn_itvl=24,.supervision_timeout=200};
    memcpy(evt.peer_addr,address,6);
    if((address[5]&0xc0)==0x40 && controller_resolving) {
        // Model controller identity resolution using the IRK actually installed
        // by host HCI commands, rather than handing the host a known identity.
        for(unsigned i=0;i<resolving_count;i++) {
            struct ble_encryption_block ecb={0};
            for(unsigned k=0;k<16;k++)ecb.key[k]=resolving_entries[i].peer_irk[15-k];
            for(unsigned k=0;k<3;k++)ecb.plain_text[15-k]=address[3+k];
            assert(!ble_hw_encrypt_block(&ecb));
            if(ecb.cipher_text[15]==address[0] && ecb.cipher_text[14]==address[1] &&
               ecb.cipher_text[13]==address[2]) {
                evt.peer_addr_type=resolving_entries[i].peer_addr_type+2;
                memcpy(evt.peer_addr,resolving_entries[i].peer_id_addr,6);
                memcpy(evt.peer_rpa,address,6);break;
            }
        }
    }
    uint8_t packet[sizeof(evt)+2]={BLE_HCI_EVCODE_LE_META,sizeof(evt)};
    for(unsigned i=0;i<8 && gopine_ble_host_work_pending();i++)gopine_ble_poll();
    uint8_t apple[ANCS_PACKET_SIZE],music[87];
    if(!retain_apple)while(gopine_ble_take_apple_notification(apple)){}
    gopine_ble_take_music(music);
    gopine_ble_ack_updates();
    if(gopine_ble_updates() && !retain_peer_data && !retain_apple && !(update_window && update_pending(&update_incoming)))
        fprintf(stderr,"pre-connect pending: handle %u update %d host %d bond %u links %u music %d ancs %d\n",
                handle,update_window,gopine_ble_host_work_pending(),gopine_bond_delay(),
                update_links_delay(&update_links,ble_npl_time_get()),music_snapshot_dirty,gopine_ancs_updates());
    assert(!gopine_ble_updates() || retain_peer_data || retain_apple || (update_window && update_pending(&update_incoming)));
    struct ble_gap_conn_desc desc;
    assert(ble_gap_conn_find(handle,&desc)==BLE_HS_ENOTCONN);
    uint16_t previous_connection=connection;
    reply_len=0;memcpy(packet+2,&evt,sizeof(evt));send_event(packet,sizeof(packet));
    gopine_ble_controller_poll();assert(connection==previous_connection);
    // Mirror Wait's controller-only pass: a raw HCI event must return control
    // to Go even before any service callback has produced an app snapshot.
    assert(gopine_ble_host_work_pending());
    assert(gopine_ble_updates());
    gopine_ble_poll();
    assert(!ble_gap_conn_find(handle,&desc));
}
static void connect_peer(void) {
    client_request_count=0;
    connect_peer_on(1,peer_address);
}
static void receive_l2cap_on(uint16_t handle,uint16_t cid,const uint8_t *value,unsigned size) {
    for(unsigned i=0;i<8 && gopine_ble_host_work_pending();i++)gopine_ble_poll();
    // The production controller disables data-length extension. Its incoming
    // HCI ACL packets therefore carry at most 27 bytes, including the initial
    // L2CAP header. Exercise actual host reassembly, particularly the 65-byte
    // Secure Connections public key and long ANCS attribute responses.
    uint8_t pdu[251];assert(size+4<=sizeof(pdu));
    put_le16(pdu,size);put_le16(pdu+2,cid);memcpy(pdu+4,value,size);
    uint8_t apple[ANCS_PACKET_SIZE],music[87];
    if(!retain_apple)while(gopine_ble_take_apple_notification(apple)){}
    gopine_ble_take_music(music);
    gopine_ble_ack_updates();
    if(gopine_ble_updates() && !retain_peer_data && !retain_apple && !(update_window && update_pending(&update_incoming)))
        fprintf(stderr,"pre-ATT pending: handle %u cid %u opcode %u update %d host %d bond %u links %u music %d\n",
                handle,cid,value[0],update_window,gopine_ble_host_work_pending(),gopine_bond_delay(),
                update_links_delay(&update_links,ble_npl_time_get()),music_snapshot_dirty);
    assert(!gopine_ble_updates() || retain_peer_data || retain_apple || (update_window && update_pending(&update_incoming)));
    reply_len=0;captured_count=0;
    for(unsigned offset=0;offset<size+4;) {
        unsigned count=size+4-offset;if(count>27)count=27;
        struct os_mbuf *om=os_msys_get_pkthdr(count+4,0);assert(om);
        uint8_t header[4];put_le16(header,handle|(offset?0x1000:0x2000));
        put_le16(header+2,count);
        assert(!os_mbuf_append(om,header,sizeof(header)));
        assert(!os_mbuf_append(om,pdu+offset,count));
        assert(!ble_hci_trans_ll_acl_tx(om));
        gopine_ble_controller_poll();
        assert(gopine_ble_host_work_pending() && gopine_ble_updates());
        if(!burst_rx)gopine_ble_poll();offset+=count;
    }
    if(burst_rx)gopine_ble_poll();
}
static void receive_l2cap(uint16_t cid,const uint8_t *value,unsigned size) {
    receive_l2cap_on(1,cid,value,size);
}
static void att_on(uint16_t handle,const uint8_t *value,unsigned size,uint8_t response) {
    receive_l2cap_on(handle,BLE_L2CAP_CID_ATT,value,size);
    if(reply_len<9 || get_le16(reply+6)!=BLE_L2CAP_CID_ATT || reply[8]!=response)
        fprintf(stderr,"ATT request %u length %u: expected %u, reply length %u opcode %u\n",
                value[0],size,response,reply_len,reply_len>=9?reply[8]:0);
    assert(reply_len>=9 && get_le16(reply+6)==BLE_L2CAP_CID_ATT);
    assert((get_le16(reply)&0x0fff)==handle);
    assert(reply[8]==response);
}
static void att(const uint8_t *value,unsigned size,uint8_t response) {
    att_on(1,value,size,response);
}
static void discover(void) {
    const uint8_t mtu[]={2,247,0};att(mtu,sizeof(mtu),3);
    // Enumerate primary services and all characteristic declarations, as
    // BlueZ does before an updater can access its custom characteristics.
    for(unsigned kind=0;kind<2;kind++) {
        uint16_t start=1;
        do {
            uint8_t request[]={kind?8:16,0,0,255,255,kind?3:0,0x28};
            put_le16(request+1,start);
            receive_l2cap(BLE_L2CAP_CID_ATT,request,sizeof(request));
            assert(reply_len>=9);
            if(reply[8]==1) {assert(reply[12]==BLE_ATT_ERR_ATTR_NOT_FOUND);break;}
            assert(reply[8]==(kind?9:17));
            unsigned length=reply[9];assert(length>=6);
            unsigned end=8+get_le16(reply+4);assert(end<=reply_len);
            unsigned last=end-length;
            start=get_le16(reply+last+(kind?0:2))+1;
        } while(start);
    }
    uint16_t handle;
    assert(ble_gatts_find_chr(BLE_UUID16_DECLARE(0x180f),BLE_UUID16_DECLARE(0x2a19),NULL,&handle)==0);
    uint8_t read[]={10,0,0};put_le16(read+1,handle);att(read,sizeof(read),11);
    assert(reply[9]==80); // Battery must not trigger pairing in update mode.
    assert(ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&handle)==0);
    put_le16(read+1,handle);att(read,sizeof(read),11);
    assert(reply_len==25 && reply[9]==2 && reply[10]==1);
}
static void update_notifications(void) {
    uint16_t control,data,status;
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[1].u,NULL,&control));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[2].u,NULL,&data));
    assert(!ble_gatts_find_chr(&update_uuids[0].u,&update_uuids[3].u,NULL,&status));
    uint8_t begin[]={BLE_ATT_OP_WRITE_REQ,0,0,1,42,0,0,0,236,0,0,0};
    put_le16(begin+1,control);
    att(begin,sizeof(begin),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED && !update_incoming.count);
    uint8_t cccd[]={BLE_ATT_OP_WRITE_REQ,0,0,1,0};put_le16(cccd+1,status+1);
    receive_l2cap(BLE_L2CAP_CID_ATT,cccd,sizeof(cccd));
    gopine_ble_poll();
    assert(reply_len==36 && reply[8]==BLE_ATT_OP_WRITE_RSP);
    memmove(reply,reply+9,27);reply_len=27;
    assert(reply[8]==BLE_ATT_OP_NOTIFY_REQ);
    assert(get_le16(reply+9)==status && reply[11]==2 && reply[12]==1 && reply[14]==244);
    reply_len=0;gopine_ble_poll();assert(reply_len==0); // No unchanged-status flood.
    att(begin,sizeof(begin),BLE_ATT_OP_WRITE_RSP);
    uint8_t packet[244];unsigned size;
    assert(gopine_ble_take_update(packet,&size)==1 && size==9);
    assert(packet[0]==1 && get_le32(packet+1)==42);
    uint8_t snapshot[16]={2,2,0,0};put_le32(snapshot+4,236);put_le32(snapshot+12,42);
    reply_len=0;gopine_ble_update_status(snapshot);gopine_ble_poll();
    assert(reply[8]==BLE_ATT_OP_NOTIFY_REQ && reply[12]==2 && get_le32(reply+19)==0);
    uint8_t write[247]={BLE_ATT_OP_WRITE_REQ};put_le16(write+1,data);
    put_le32(write+3,42);memset(write+11,0x5a,236);
    att(write,sizeof(write),BLE_ATT_OP_WRITE_RSP);
    // Accepting a packet does not acknowledge flash; duplicate offsets are rejected.
    att(write,sizeof(write),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_WRITE_NOT_PERMITTED);
    assert(gopine_ble_take_update(packet,&size)==2 && size==244 && packet[243]==0x5a);
    reply_len=0;gopine_ble_poll();assert(reply_len==0);
    put_le32(snapshot+8,236);reply_len=0;gopine_ble_update_status(snapshot);gopine_ble_poll();
    assert(reply[8]==BLE_ATT_OP_NOTIFY_REQ && get_le32(reply+19)==236 && reply[14]==244);
    snapshot[1]=4;reply_len=0;gopine_ble_update_status(snapshot);gopine_ble_poll();
    assert(reply[8]==BLE_ATT_OP_NOTIFY_REQ && reply[12]==4);
    reply_len=0;gopine_ble_update_status(snapshot);gopine_ble_poll();assert(reply_len==0);
    // An exhausted notification pool cannot discard a durable acknowledgement.
    struct os_mbuf *held[64];unsigned count=0;
    while(count<64 && (held[count]=os_msys_get_pkthdr(16,0)))count++;
    assert(count<64);
    snapshot[1]=5;snapshot[2]=1;reply_len=0;
    gopine_ble_update_status(snapshot);gopine_ble_poll();
    assert(reply_len==0);
    while(count)os_mbuf_free_chain(held[--count]);
    gopine_ble_poll();
    assert(reply_len==27 && reply[8]==BLE_ATT_OP_NOTIFY_REQ && reply[12]==5);
}
static void phone_pairing_code(bool timeout) {
    uint8_t time[10]={0};
    assert(gopine_ble_start_phone(time,80,0)==0);gopine_ble_poll();connect_peer();
    assert(phone_window && !update_window && !phone_peer_find(1)->security_pending);
    assert(reply_len==0); // First pairing follows the central's protected ATT/ANCS request.
    uint16_t handle;
    assert(ble_gatts_find_chr(BLE_UUID16_DECLARE(0x180f),BLE_UUID16_DECLARE(0x2a19),NULL,&handle)==0);
    uint8_t read[]={10,0,0};put_le16(read+1,handle);att(read,sizeof(read),BLE_ATT_OP_READ_RSP);
    assert(reply_len==10 && reply[9]==80 && !gopine_sm_progress(1));
    uint8_t pairing[]={BLE_SM_OP_PAIR_REQ,BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,16,3,3};
    receive_l2cap(BLE_L2CAP_CID_SM,pairing,sizeof(pairing));
    assert(reply_len>=9 && reply[8]==BLE_SM_OP_PAIR_RSP);
    uint8_t private[32]={0},public[64],packet[65]={BLE_SM_OP_PAIR_PUBLIC_KEY};
    private[31]=3;
    assert(uECC_compute_public_key(private,public,uECC_secp256r1())==1);
    for(unsigned i=0;i<32;i++) {packet[1+i]=public[31-i];packet[33+i]=public[63-i];}
    receive_l2cap(BLE_L2CAP_CID_SM,packet,sizeof(packet));
    assert(pairing_code>0 && pairing_code<=1000000 && !pairing_io_pending);
    assert(connection==1);
    if(!timeout) {
        const uint8_t other[]={4,3,2,1,9,0xc4};
        uint32_t first_code=pairing_code;uint16_t first_progress=gopine_sm_progress(1);
        unsigned crypto_before=crypto_yields;
        connect_peer_on(2,other);
        receive_l2cap_on(2,BLE_L2CAP_CID_SM,pairing,sizeof(pairing));
        assert(reply[8]==BLE_SM_OP_PAIR_FAIL && reply[9]==BLE_SM_ERR_AUTHREQ);
        assert(pairing_code==first_code && gopine_sm_progress(1)==first_progress &&
               !gopine_sm_progress(2) && crypto_yields==crypto_before);
    }
    if(timeout) {
        assert(gopine_ble_pairing_progress()==0x200);
        gopine_ble_ack_updates();
        test_rtc0.COUNTER+=31u*32768;
        assert(gopine_ble_host_work_pending() && gopine_ble_updates());
        gopine_ble_poll();
        assert(!pairing_code && !gopine_bond_exists());
        assert(gopine_ble_pairing_diagnostics()==BLE_HS_ETIMEOUT);
        assert(gopine_ble_pairing_progress()==0x200);
    }
    gopine_ble_stop();gopine_ble_poll();
    assert(!gopine_ble_busy() && pairing_code==0);
}
// Service discovery and SMP use separate L2CAP channels. Keep the four
// controller TX slots occupied until a later connection event instead of
// immediately releasing every fragment as the original fake controller did.
static void phone_retry_advertising(void) {
    uint8_t time[10]={0};assert(!gopine_ble_forget_phone());
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    assert(advertised_min==32 && advertised_max==48);
    connect_peer();uint32_t generation=peer_generation;
    gopine_ble_phone_setup();
    assert(connection==1 && peer_generation==generation); // Never cancel SMP.
    uint8_t disconnected[]={BLE_HCI_EVCODE_DISCONN_CMP,4,0,1,0,BLE_ERR_CONN_SPVN_TMO};
    send_event(disconnected,sizeof(disconnected));gopine_ble_poll();
    assert(connection==BLE_HS_CONN_HANDLE_NONE && ble_gap_adv_active());
    assert(!slow_advertising && advertised_min==32 && advertised_max==48);
    assert((gopine_ble_pairing_diagnostics()>>16)==BLE_HS_HCI_ERR(BLE_ERR_CONN_SPVN_TMO));
    test_rtc0.COUNTER+=31u*32768;gopine_ble_poll();
    assert(slow_advertising && advertised_min==1600 && advertised_max==2400);
    gopine_ble_phone_setup();
    assert(!slow_advertising && advertised_min==32 && advertised_max==48);
    struct ble_gap_event failed={.type=BLE_GAP_EVENT_CONNECT};
    failed.connect.status=BLE_HS_HCI_ERR(BLE_ERR_CONN_SPVN_TMO);
    // A failed establishment must survive even when no successful CONNECT or
    // subsequent DISCONNECT can publish a diagnostic.
    assert(!ble_gap_adv_stop());gap_event(&failed,NULL);gopine_ble_poll();
    assert((uint16_t)gopine_ble_pairing_diagnostics()==failed.connect.status);
    assert(ble_gap_adv_active());
    // A handoff to OTA preserves the phone trace without requesting SMP.
    unsigned count=trace_count;
    assert(count>=3 && pairing_trace[0]==1);
    gopine_ble_stop();gopine_ble_poll();
    assert(!gopine_ble_start_update(80));gopine_ble_poll();connect_peer();
    uint16_t trace_handle;assert(!ble_gatts_find_chr(&pairing_trace_service.u,&pairing_trace_characteristic.u,NULL,&trace_handle));
    uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,trace_handle);
    att(read,sizeof(read),BLE_ATT_OP_READ_RSP);
    assert(reply[9]==1 && reply[10]==trace_count && trace_count>=count);
    // Read the complete, saturated diagnostic ring at the default MTU, as
    // BlueZ's long read does, rather than checking only the first 22 bytes.
    while(trace_count<24)trace_pairing(13,0x1005);
    uint8_t expected[196],received[196];memcpy(expected,pairing_trace,sizeof(expected));
    unsigned copied=0;
    do {
        uint8_t blob[]={BLE_ATT_OP_READ_BLOB_REQ,0,0,0,0};
        put_le16(blob+1,trace_handle);put_le16(blob+3,copied);
        if(!copied)att(read,sizeof(read),BLE_ATT_OP_READ_RSP);
        else att(blob,sizeof(blob),BLE_ATT_OP_READ_BLOB_RSP);
        unsigned n=get_le16(reply+4)-1;
        assert(n<=22 && copied+n<=sizeof(received));
        memcpy(received+copied,reply+9,n);copied+=n;
        if(n<22)break;
    } while(copied<sizeof(received));
    assert(copied==sizeof(expected) && !memcmp(received,expected,copied));
    // OTA encryption must not replace the retained Phone failure or stage.
    uint32_t diagnostic=gopine_ble_pairing_diagnostics();
    uint16_t progress=pairing_progress;
    struct ble_gap_event encrypted={.type=BLE_GAP_EVENT_ENC_CHANGE};
    encrypted.enc_change.conn_handle=1;
    gap_event(&encrypted,NULL);
    assert(gopine_ble_pairing_diagnostics()==diagnostic && pairing_progress==progress);
    assert(!memcmp(pairing_trace,expected,sizeof(expected)));
    gopine_ble_stop();gopine_ble_poll();
}
static void phone_pairing_under_discovery(void) {
    uint8_t time[10]={0};
    assert(!gopine_ble_forget_phone());
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();connect_peer();
    uint8_t mtu[]={BLE_ATT_OP_MTU_REQ,247,0};att(mtu,sizeof(mtu),BLE_ATT_OP_MTU_RSP);
    uint8_t pairing[]={BLE_SM_OP_PAIR_REQ,BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,16,3,3};
    receive_l2cap(BLE_L2CAP_CID_SM,pairing,sizeof(pairing));
    assert(reply[8]==BLE_SM_OP_PAIR_RSP);
    uint8_t private[32]={0},public[64],packet[65]={BLE_SM_OP_PAIR_PUBLIC_KEY};
    private[31]=3;assert(uECC_compute_public_key(private,public,uECC_secp256r1()));
    for(unsigned i=0;i<32;i++){packet[1+i]=public[31-i];packet[33+i]=public[63-i];}
    uint16_t music_start;
    assert(!ble_gatts_find_svc(&music_uuids[0].u,&music_start));
    uint8_t discovery[]={BLE_ATT_OP_READ_TYPE_REQ,0,0,255,255,3,0x28};
    put_le16(discovery+1,music_start);
    defer_acl=true;
    receive_l2cap(BLE_L2CAP_CID_ATT,discovery,sizeof(discovery));
    assert(pending_acl_count==4);
    burst_rx=true;
    receive_l2cap(BLE_L2CAP_CID_SM,packet,sizeof(packet));
    burst_rx=false;
    assert(os_msys_num_free()>=1);
    assert(pairing_code>0 && !last_security_status);
    defer_acl=false;
    // A connection event returns TX ownership/credits; flush any host backlog.
    for(unsigned i=0;i<pending_acl_count;i++)assert(!ble_ll_hci_acl_rx(pending_acl[i],NULL));
    pending_acl_count=0;
    gopine_ble_poll();
    gopine_ble_stop();gopine_ble_poll();
    assert(!gopine_ble_busy());
}
static void request_ltk_on(uint16_t handle,const uint8_t *expected_ltk) {
    struct ble_hci_ev_le_subev_lt_key_req request={
        .subev_code=BLE_HCI_LE_SUBEV_LT_KEY_REQ,.conn_handle=handle};
    uint8_t packet[sizeof(request)+2]={BLE_HCI_EVCODE_LE_META,sizeof(request)};
    memcpy(packet+2,&request,sizeof(request));
    unsigned before=ltk_replies;send_event(packet,sizeof(packet));gopine_ble_poll();
    assert(ltk_replies==before+1 && !memcmp(controller_ltk,expected_ltk,16));
}
static void encryption_complete_on(uint16_t handle) {
    uint8_t encrypted[]={BLE_HCI_EVCODE_ENCRYPT_CHG,4,0,1,0,1};
    put_le16(encrypted+3,handle);
    reply_len=0;send_event(encrypted,sizeof(encrypted));gopine_ble_poll();
}
static void request_encryption_on(uint16_t handle,const uint8_t *expected_ltk) {
    request_ltk_on(handle,expected_ltk);encryption_complete_on(handle);
}
static void request_encryption(const uint8_t *expected_ltk) {
    request_encryption_on(1,expected_ltk);
}
static void assert_authenticated(void) {
    struct ble_gap_conn_desc desc;
    assert(ble_gap_conn_find(1,&desc)==0);
    assert(desc.sec_state.encrypted && desc.sec_state.authenticated &&
           desc.sec_state.bonded && desc.sec_state.key_size==16);
    assert(phone_link_permission(1)==0 && pairing_code==0);
}
static void write_characteristic_on(uint16_t connection_handle,const ble_uuid_t *service,
                                    const ble_uuid_t *characteristic,const uint8_t *data,
                                    unsigned size,uint8_t response) {
    uint16_t handle;assert(ble_gatts_find_chr(service,characteristic,NULL,&handle)==0);
    uint8_t packet[56]={BLE_ATT_OP_WRITE_REQ};assert(size<=sizeof(packet)-3);
    put_le16(packet+1,handle);memcpy(packet+3,data,size);
    att_on(connection_handle,packet,size+3,response);
}
static void write_characteristic(const ble_uuid_t *service,const ble_uuid_t *characteristic,
                                 const uint8_t *data,unsigned size,uint8_t response) {
    write_characteristic_on(1,service,characteristic,data,size,response);
}
static void phone_feature_traffic(void) {
    uint8_t out[147];uint32_t age;
    const uint8_t *times[]={companion_time_9,companion_time_10};
    const unsigned time_sizes[]={sizeof(companion_time_9),sizeof(companion_time_10)};
    for(unsigned i=0;i<2;i++) {
        write_characteristic(BLE_UUID16_DECLARE(0x1805),BLE_UUID16_DECLARE(0x2a2b),
                             times[i],time_sizes[i],BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take(out,&age)==(int)time_sizes[i] && !memcmp(times[i],out,time_sizes[i]));
    }
    const uint8_t *weather[]={companion_current_v0,companion_current_v1,companion_forecast};
    const unsigned weather_sizes[]={sizeof(companion_current_v0),sizeof(companion_current_v1),sizeof(companion_forecast)};
    for(unsigned i=0;i<3;i++) {
        write_characteristic(&weather_uuid.u,&weather_data_uuid.u,weather[i],weather_sizes[i],BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_weather(out)==(int)weather_sizes[i] && !memcmp(weather[i],out,weather_sizes[i]));
    }
    write_characteristic(&music_uuids[0].u,&music_uuids[2].u,companion_status,sizeof(companion_status),BLE_ATT_OP_WRITE_RSP);
    write_characteristic(&music_uuids[0].u,&music_uuids[4].u,companion_track,sizeof(companion_track),BLE_ATT_OP_WRITE_RSP);
    write_characteristic(&music_uuids[0].u,&music_uuids[3].u,companion_artist,sizeof(companion_artist),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_music(out) && out[81]==PHONE_LINK_MUSIC_READY &&
           out[80]==1 && !memcmp(out,companion_track,sizeof(companion_track)) &&
           !memcmp(out+40,companion_artist,sizeof(companion_artist)));
    const uint8_t *numbers[]={companion_position,companion_duration};
    for(unsigned i=0;i<2;i++) {
        write_characteristic(&music_uuids[0].u,&music_uuids[6+i].u,numbers[i],4,BLE_ATT_OP_WRITE_RSP);
        uint16_t handle;assert(!ble_gatts_find_chr(&music_uuids[0].u,&music_uuids[6+i].u,NULL,&handle));
        uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,handle);
        att(read,sizeof(read),BLE_ATT_OP_READ_RSP);assert(!memcmp(reply+9,numbers[i],4));
    }
    const uint8_t controls[]={0xe0,0,1,3,4,5,6};
    for(unsigned i=0;i<sizeof(controls);i++) {
        reply_len=0;
        assert(!gopine_ble_music_command(controls[i],peer_generation));
        assert(reply_len==12 && reply[8]==BLE_ATT_OP_NOTIFY_REQ &&
               get_le16(reply+9)==music_handle && reply[11]==controls[i]);
        gopine_ble_poll();
    }
    reply_len=0;assert(gopine_ble_music_command(0,peer_generation-1)==BLE_HS_ENOTCONN && !reply_len);
    write_characteristic(BLE_UUID16_DECLARE(0x1811),BLE_UUID16_DECLARE(0x2a46),
                         companion_alert,sizeof(companion_alert),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_notification(out)==sizeof(companion_alert) &&
           !memcmp(out,companion_alert,sizeof(companion_alert)));
    assert(phone_window && window && connection==1 && !expired());
}
#include "host_ancs.h"
#include "host_service_changed.h"
#include "host_ota_connections.h"
#include "host_update_pipeline.h"
#include "host_phone_connections.h"
// Follow the peer-initiated passkey sequence in InfiniTime's pinned NimBLE
// ble_sm_test_util_peer_sc_good_once_no_init, using a dynamically calculated
// central transcript. No SM internal state is injected or bypassed.
static void phone_complete_pairing(bool wrong_code, bool fail_identity) {
    uint8_t time[10]={0};assert(!gopine_bond_exists());
    // Pair over a temporary private address, then distribute a different
    // stable identity, as an iPhone does. Store subscriptions under identity.
    peer_address[5]=0x40;
    assert(gopine_ble_start_phone(time,80,0)==0);gopine_ble_poll();connect_peer();
    uint16_t cccd_handle;
    uint8_t mtu[]={BLE_ATT_OP_MTU_REQ,185,0};att(mtu,sizeof(mtu),BLE_ATT_OP_MTU_RSP);
    // InfiniLink reads Battery once during characteristic discovery, then
    // subscribes. This public read must not itself initiate SMP or require a
    // retry after bonding; classic Home readiness is covered by the DIS test.
    uint8_t battery_read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(battery_read+1,battery_handle);
    att(battery_read,sizeof(battery_read),BLE_ATT_OP_READ_RSP);
    assert(reply_len==10 && reply[9]==80 && !gopine_sm_progress(1) && !pairing_code);
    assert(!ble_gatts_find_dsc(BLE_UUID16_DECLARE(0x180f),BLE_UUID16_DECLARE(0x2a19),
                             BLE_UUID16_DECLARE(0x2902),&cccd_handle));
    uint8_t battery_subscribe[]={BLE_ATT_OP_WRITE_REQ,0,0,1,0};put_le16(battery_subscribe+1,cccd_handle);
    att(battery_subscribe,sizeof(battery_subscribe),BLE_ATT_OP_WRITE_RSP);
    assert(!phone_link_ready(1) && !gopine_bond_exists());
    assert(ble_gatts_find_dsc(&music_uuids[0].u,&music_uuids[1].u,
                            BLE_UUID16_DECLARE(0x2902),&cccd_handle)==0);
    uint8_t subscribe[]={BLE_ATT_OP_WRITE_REQ,0,0,1,0};put_le16(subscribe+1,cccd_handle);
    att(subscribe,sizeof(subscribe),BLE_ATT_OP_WRITE_RSP);
    assert(phone_peer_find(1)->music_subscribed && phone_link_snapshot(1,true)==PHONE_LINK_SECURING);
    uint8_t early_time[10]={0xea,0x07,10,9,16,7,8,5,0,1};
    write_characteristic(BLE_UUID16_DECLARE(0x1805),BLE_UUID16_DECLARE(0x2a2b),
                         early_time,sizeof(early_time),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_AUTHEN && !phone_data(phone_peer_find(1))->time.size);
    write_characteristic(&weather_uuid.u,&weather_data_uuid.u,
                         companion_current_v1,sizeof(companion_current_v1),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_AUTHEN && !phone_data(phone_peer_find(1))->weather.sizes[0]);
    write_characteristic(&music_uuids[0].u,&music_uuids[4].u,
                         companion_track,sizeof(companion_track),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_AUTHEN && !phone_data(phone_peer_find(1))->music.track[0]);
    write_characteristic(BLE_UUID16_DECLARE(0x1811),BLE_UUID16_DECLARE(0x2a46),
                         companion_alert,sizeof(companion_alert),BLE_ATT_OP_ERROR_RSP);
    assert(reply[12]==BLE_ATT_ERR_INSUFFICIENT_AUTHEN && !phone_data(phone_peer_find(1))->notification.count);
    uint8_t pairing[]={BLE_SM_OP_PAIR_REQ,BLE_SM_IO_CAP_KEYBOARD_DISP,0,0x0d,16,3,3};
    receive_l2cap(BLE_L2CAP_CID_SM,pairing,sizeof(pairing));
    assert(reply[8]==BLE_SM_OP_PAIR_RSP);
    uint8_t response[7];memcpy(response,reply+8,sizeof(response));
    uint8_t private_be[32]={0},private_le[32]={3},public_be[64];private_be[31]=3;
    assert(uECC_compute_public_key(private_be,public_be,uECC_secp256r1())==1);
    uint8_t public[65]={BLE_SM_OP_PAIR_PUBLIC_KEY},watch_public[64];
    for(unsigned i=0;i<32;i++) {public[1+i]=public_be[31-i];public[33+i]=public_be[63-i];}
    receive_l2cap(BLE_L2CAP_CID_SM,public,sizeof(public));
    assert(reply[8]==BLE_SM_OP_PAIR_PUBLIC_KEY && pairing_code>0);
    assert(gopine_ble_pairing_progress()==0x200);
    memcpy(watch_public,reply+9,sizeof(watch_public));
    uint32_t passkey=pairing_code-1;
    uint8_t nonce[16],watch_nonce[16],watch_confirm[16],confirm[17],random[17];
    for(unsigned round=0;round<20;round++) {
        memset(nonce,round+1,sizeof(nonce));
        uint8_t bit=0x80|((passkey>>round)&1);
        confirm[0]=BLE_SM_OP_PAIR_CONFIRM;
        assert(ble_sm_alg_f4(public+1,watch_public,nonce,bit,confirm+1)==0);
        if(wrong_code && round==0)confirm[1]^=1;
        receive_l2cap(BLE_L2CAP_CID_SM,confirm,sizeof(confirm));
        assert(reply[8]==BLE_SM_OP_PAIR_CONFIRM);
        assert(gopine_ble_pairing_progress()==(0x280|round));
        memcpy(watch_confirm,reply+9,16);
        random[0]=BLE_SM_OP_PAIR_RANDOM;memcpy(random+1,nonce,16);
        receive_l2cap(BLE_L2CAP_CID_SM,random,sizeof(random));
        if(wrong_code) {
            assert(reply[8]==BLE_SM_OP_PAIR_FAIL && reply[9]==BLE_SM_ERR_CONFIRM_MISMATCH);
            assert(pairing_code==0 && !gopine_bond_exists());
            assert(gopine_ble_pairing_progress()==0x280);
            assert(gopine_ble_pairing_diagnostics()==BLE_HS_SM_US_ERR(BLE_SM_ERR_CONFIRM_MISMATCH));
            uint8_t disconnected[]={BLE_HCI_EVCODE_DISCONN_CMP,4,0,1,0,BLE_ERR_REM_USER_CONN_TERM};
            send_event(disconnected,sizeof(disconnected));gopine_ble_poll();
            assert(gopine_ble_pairing_progress()==0x280);
            assert(gopine_ble_pairing_diagnostics()==
                (BLE_HS_SM_US_ERR(BLE_SM_ERR_CONFIRM_MISMATCH)|
                 ((uint32_t)BLE_HS_HCI_ERR(BLE_ERR_REM_USER_CONN_TERM)<<16)));
            gopine_ble_stop();gopine_ble_poll();return;
        }
        assert(reply[8]==BLE_SM_OP_PAIR_RANDOM && pairing_code==passkey+1);
        assert(gopine_ble_pairing_progress()==(round==19?0x314:(0x200|round+1)));
        memcpy(watch_nonce,reply+9,16);
        uint8_t expected[16];
        assert(ble_sm_alg_f4(watch_public,public+1,watch_nonce,bit,expected)==0);
        assert(!memcmp(expected,watch_confirm,16));
    }
    struct ble_gap_conn_desc desc;assert(ble_gap_conn_find(1,&desc)==0);
    uint8_t dhkey[32],mackey[16],ltk[16],tk[16]={0},check[17]={BLE_SM_OP_PAIR_DHKEY_CHECK};
    put_le32(tk,passkey);
    assert(ble_sm_alg_gen_dhkey(watch_public,watch_public+32,private_le,dhkey)==0);
    assert(ble_sm_alg_f5(dhkey,nonce,watch_nonce,desc.peer_ota_addr.type,desc.peer_ota_addr.val,
                        desc.our_ota_addr.type,desc.our_ota_addr.val,mackey,ltk)==0);
    assert(ble_sm_alg_f6(mackey,nonce,watch_nonce,tk,pairing+1,
                        desc.peer_ota_addr.type,desc.peer_ota_addr.val,
                        desc.our_ota_addr.type,desc.our_ota_addr.val,check+1)==0);
    receive_l2cap(BLE_L2CAP_CID_SM,check,sizeof(check));
    assert(reply[8]==BLE_SM_OP_PAIR_DHKEY_CHECK);
    assert(gopine_ble_pairing_progress()==0x414);
    uint8_t expected[16];
    assert(ble_sm_alg_f6(mackey,watch_nonce,nonce,tk,response+1,
                        desc.our_ota_addr.type,desc.our_ota_addr.val,
                        desc.peer_ota_addr.type,desc.peer_ota_addr.val,expected)==0);
    assert(!memcmp(reply+9,expected,16));
    request_encryption(ltk);
    assert(gopine_ble_pairing_progress()==0x514);
    // Encryption precedes identity distribution. No Battery read retry is
    // made here: the companion already received its value before pairing.
    assert(!gopine_bond_exists());
    assert(!ble_gap_conn_find(1,&desc) && desc.sec_state.encrypted &&
           desc.sec_state.authenticated && !desc.sec_state.bonded);
    // Secure Connections derives the LTK; phase 3 distributes identity keys.
    // ATT retries may precede those keys. Authentication has succeeded, so
    // don't ask iOS to authenticate again; retain data until bonding finishes.
    write_characteristic(BLE_UUID16_DECLARE(0x1805),BLE_UUID16_DECLARE(0x2a2b),
                         early_time,sizeof(early_time),BLE_ATT_OP_WRITE_RSP);
    uint8_t deferred[147];uint32_t deferred_age;
    assert(!gopine_ble_take(deferred,&deferred_age) && phone_data(phone_peer_find(1))->time.size);
    write_characteristic(&weather_uuid.u,&weather_data_uuid.u,
                         companion_current_v1,sizeof(companion_current_v1),BLE_ATT_OP_WRITE_RSP);
    assert(!gopine_ble_take_weather(deferred) && phone_data(phone_peer_find(1))->weather.sizes[0]);
    write_characteristic(&music_uuids[0].u,&music_uuids[4].u,
                         companion_track,sizeof(companion_track),BLE_ATT_OP_WRITE_RSP);
    assert(gopine_ble_take_music(deferred) && deferred[81]==PHONE_LINK_SECURING && !deferred[0]);
    assert(!memcmp(phone_data(phone_peer_find(1))->music.track,companion_track,sizeof(companion_track)));
    assert(gopine_ble_music_command(0,peer_generation)==BLE_HS_ENOTCONN);
    write_characteristic(BLE_UUID16_DECLARE(0x1811),BLE_UUID16_DECLARE(0x2a46),
                         companion_alert,sizeof(companion_alert),BLE_ATT_OP_WRITE_RSP);
    assert(!gopine_ble_take_notification(deferred) && phone_data(phone_peer_find(1))->notification.count);
    gopine_ble_ack_updates();assert(!gopine_ble_updates()); // no pending-data spin
    assert(phone_link_snapshot(1,true)==PHONE_LINK_SECURING);
    assert(!client_request_count); // ANCS waits for completed bonding too.
    if(fail_identity) {
        uint8_t failed[]={BLE_SM_OP_PAIR_FAIL,BLE_SM_ERR_UNSPECIFIED};
        receive_l2cap(BLE_L2CAP_CID_SM,failed,sizeof(failed));
        assert(!pairing_code && !gopine_bond_exists() && !phone_link_ready(1));
        assert(!phone_data(phone_peer_find(1))->time.size && !phone_data(phone_peer_find(1))->weather.sizes[0] &&
               !phone_data(phone_peer_find(1))->notification.count && !phone_data(phone_peer_find(1))->music.track[0]);
        assert(gopine_ble_pairing_progress()==0x514);
        gopine_ble_stop();test_rtc0.COUNTER+=32768;gopine_ble_poll();gopine_ble_poll();
        assert(!gopine_ble_busy());return;
    }
    uint8_t identity[17]={BLE_SM_OP_IDENTITY_INFO};memset(identity+1,0x55,16);
    receive_l2cap(BLE_L2CAP_CID_SM,identity,sizeof(identity));
    uint8_t address[]={BLE_SM_OP_IDENTITY_ADDR_INFO,BLE_ADDR_RANDOM,1,2,3,4,5,0xc0};
    receive_l2cap(BLE_L2CAP_CID_SM,address,sizeof(address));
    assert(gopine_ble_take(deferred,&deferred_age)==sizeof(early_time) &&
           !memcmp(deferred,early_time,sizeof(early_time)));
    assert(gopine_ble_take_weather(deferred)==sizeof(companion_current_v1) &&
           !memcmp(deferred,companion_current_v1,sizeof(companion_current_v1)));
    assert(gopine_ble_take_notification(deferred)==sizeof(companion_alert) &&
           !memcmp(deferred,companion_alert,sizeof(companion_alert)));
    assert(gopine_ble_take_music(deferred) && deferred[81]==PHONE_LINK_MUSIC_READY &&
           !memcmp(deferred,companion_track,sizeof(companion_track)));
    assert_authenticated();
    service_changed_subscribe();
    assert(gopine_ble_pairing_progress()==0x600);
    assert(gopine_ble_pairing_diagnostics()==0);
    assert(trace_bond_flags(BLE_ADDR_ANY)==0x1f1f);
    struct ble_store_key_cccd cccd_key={.peer_addr={BLE_ADDR_RANDOM,{1,2,3,4,5,0xc0}},
                                      .chr_val_handle=music_handle};
    struct ble_store_value_cccd cccd;
    assert(ble_store_read_cccd(&cccd_key,&cccd)==0 && cccd.flags==1);
    cccd_key.peer_addr.val[5]=0x40;
    assert(ble_store_read_cccd(&cccd_key,&cccd)==BLE_HS_ENOENT);
    phone_feature_traffic();
    phone_ancs_traffic(true,true);
    // A quiet, connected watch waits in the Go input loop, which services
    // only the controller. A pending CCCD journal save must wake that loop
    // when its own deadline arrives, without another ATT packet or UI event.
    // Exercise both unsubscribe and resubscribe through the actual ATT path.
    for(unsigned subscribed=0;subscribed<2;subscribed++) {
        subscribe[3]=subscribed;
        att(subscribe,sizeof(subscribe),BLE_ATT_OP_WRITE_RSP);
        gopine_ble_take_music(deferred);
        gopine_ble_ack_updates();
        unsigned before_save=flash_writes;
        assert(gopine_bond_status()==1 && gopine_bond_delay()==1000);
        assert(!gopine_ble_updates());
        test_rtc0.COUNTER+=16384;
        gopine_ble_controller_poll();
        assert(gopine_bond_delay()==500 && flash_writes==before_save);
        assert(!gopine_ble_updates());
        test_rtc0.COUNTER+=16384;
        gopine_ble_controller_poll();
        assert(!gopine_ble_host_work_pending());
        assert(gopine_bond_delay()==0 && gopine_ble_idle_ms()==0);
        assert(gopine_ble_updates());
        gopine_ble_poll();
        assert(gopine_bond_status()==2 && flash_writes>before_save);
        assert(connection==1 && phone_link_ready(1));
        gopine_ble_ack_updates();
        assert(!gopine_ble_updates() && gopine_ble_idle_ms()>0);
    }
    gopine_ble_stop();
    // Feature writes retain a 200 ms final-ACK grace before radio teardown.
    test_rtc0.COUNTER+=32768;gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2);
    assert_stopped_idle();
    // Reload the actual flash journal, then restore encryption with the saved
    // key. A reconnect must not ask for another code or merely read old RAM.
    gopine_bond_init();assert(gopine_bond_status()==2);
    unsigned saved_writes=flash_writes;
    // Fail a controller restore, then recover without losing the saved phone.
    reject_peer_irk=true;
    assert(gopine_ble_start_phone(time,80,0)==0);gopine_ble_poll();
    if(synced || ble_gap_adv_active() || gopine_ble_error()!=BLE_HS_HCI_ERR(BLE_ERR_MEM_CAPACITY))
        fprintf(stderr,"restore failure: synced %d advertising %d status %d expected %d\n",synced,
                ble_gap_adv_active(),gopine_ble_error(),BLE_HS_HCI_ERR(BLE_ERR_MEM_CAPACITY));
    assert(!synced && !ble_gap_adv_active() && gopine_ble_error()==BLE_HS_HCI_ERR(BLE_ERR_MEM_CAPACITY));
    assert((uint16_t)gopine_ble_pairing_diagnostics()==BLE_HS_HCI_ERR(BLE_ERR_MEM_CAPACITY));
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2 && flash_writes==saved_writes);
    reject_peer_irk=false;
    // Upstream restored the entry, but a checked replacement can also fail.
    // Preserve its HCI reason and the saved bond instead of advertising with
    // an unverified controller state or silently erasing phone keys.
    reject_peer_irk_remove=true;
    assert(gopine_ble_start_phone(time,80,0)==0);gopine_ble_poll();
    assert(!synced && !ble_gap_adv_active() &&
           gopine_ble_error()==BLE_HS_HCI_ERR(BLE_ERR_CMD_DISALLOWED));
    assert(trace_count && pairing_trace[4+(trace_count-1)*8+4]==18 &&
           get_le16(pairing_trace+4+(trace_count-1)*8+6)==BLE_HS_HCI_ERR(BLE_ERR_CMD_DISALLOWED));
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2 && flash_writes==saved_writes);
    reject_peer_irk_remove=false;
    for(unsigned rotation=0;rotation<8;rotation++) {
        // Generate a new RPA from the distributed IRK (0x55). It must resolve
        // using the controller key installed after each actual HCI Reset.
        struct ble_encryption_block rpa={0};memset(rpa.key,0x55,16);
        peer_address[3]=0x31+rotation;peer_address[4]=0x72;peer_address[5]=0x4a;
        for(unsigned k=0;k<3;k++)rpa.plain_text[15-k]=peer_address[3+k];
        assert(!ble_hw_encrypt_block(&rpa));
        for(unsigned k=0;k<3;k++)peer_address[k]=rpa.cipher_text[15-k];
        assert(gopine_ble_start_phone(time,80,0)==0);gopine_ble_poll();
        assert(synced && ble_gap_adv_active() && !gopine_ble_error());
        connect_peer();
        assert(ble_gap_conn_find(1,&desc)==0);
        assert(desc.peer_id_addr.type==BLE_ADDR_RANDOM && desc.peer_id_addr.val[5]==0xc0);
        assert(!memcmp(desc.peer_ota_addr.val,peer_address,6));
        att(mtu,sizeof(mtu),BLE_ATT_OP_MTU_RSP);
        // InfiniLink makes the same single read on reconnect. Restoration may
        // already be requested, but receiving Battery must not wait for it.
        att(battery_read,sizeof(battery_read),BLE_ATT_OP_READ_RSP);
        assert(reply_len==10 && reply[9]==80 && !phone_link_ready(1));
        request_encryption(ltk);assert_authenticated();
        assert(phone_peer_find(1)->music_subscribed && phone_link_snapshot(1,true)==PHONE_LINK_MUSIC_READY);
        phone_feature_traffic();
        phone_ancs_traffic(rotation==0,false);
        gopine_ble_stop();test_rtc0.COUNTER+=32768;gopine_ble_poll();gopine_ble_poll();
        assert(!gopine_ble_busy() && gopine_bond_status()==2 && flash_writes==saved_writes);
    }
    service_changed_reboot_fixture();
    ota_connections(ltk);
    phone_connections(ltk);
    assert(gopine_ble_forget_phone()==0);
    assert(gopine_ble_start_phone(time,80,0)==0);gopine_ble_poll();
    for(unsigned i=0;i<resolving_count;i++)assert(resolving_entries[i].peer_id_addr[5]!=0xc0);
    gopine_ble_stop();gopine_ble_poll();
}
#include "host_pairing_policy.h"
#include "host_trace_transport.h"
#include "host_device_info.h"
#include "host_update_tuning.h"
#include "host_prepare_writes.h"
#include "host_mode_payload.h"
// Compare the bounded formatter with the pinned implementation, including
// leading zeroes, UUID endianness, unknown types and the terminator boundary.
extern char *gopine_upstream_uuid_to_str(const ble_uuid_t *, char *);
static void uuid_formatting(void) {
    uint32_t random=1;
    for(unsigned i=0;i<65536;i++) {
        ble_uuid16_t short_uuid=BLE_UUID16_INIT(i);
        ble_uuid32_t word_uuid=BLE_UUID32_INIT(random);
        ble_uuid128_t long_uuid=BLE_UUID128_INIT(0);
        for(unsigned j=0;j<16;j++) {
            random=random*1664525u+1013904223u;
            long_uuid.value[j]=random>>24;
        }
        ble_uuid_t unknown={0};
        const ble_uuid_t *cases[]={&short_uuid.u,&word_uuid.u,&long_uuid.u,&unknown};
        for(unsigned j=0;j<4;j++) {
            char expected[40],actual[40];
            memset(expected,0x5a,sizeof(expected));memset(actual,0x5a,sizeof(actual));
            assert(gopine_upstream_uuid_to_str(cases[j],expected+1)==expected+1);
            assert(ble_uuid_to_str(cases[j],actual+1)==actual+1);
            assert(!memcmp(expected,actual,sizeof(actual)));
        }
    }
    puts("UUID formatting: 262144 upstream comparisons and buffer guards passed");
}

static void assert_stopped_idle(void) {
    assert(!gopine_ble_busy() && !crypto_peripheral_enabled);
    // The normal Go loop consumes the two possible ANCS session-clear records
    // even with Bluetooth off. These are one-time UI work, not leaked radio work.
    uint8_t out[ANCS_PACKET_SIZE];
    for(unsigned i=0;i<3;i++)gopine_ble_take_apple_notification(out);
    gopine_ble_ack_updates();
    unsigned before=commands;
    // Simulate the hardware input loop's controller-only pump during an hour
    // asleep. Stale software callouts must not wake Go or restart the radio.
    for(unsigned minute=0;minute<60;minute++) {
        test_rtc0.COUNTER+=60*32768;
        gopine_ble_controller_poll();
        assert(!gopine_ble_updates() && gopine_ble_idle_ms()==240000);
        assert(!gopine_ble_busy() && !crypto_peripheral_enabled);
        assert(commands==before);
    }
}

int main(int argc,char **argv) {
    setbuf(stdout,NULL);
    if(argc==3 && !strcmp(argv[1],"--service-changed-reboot"))
        return service_changed_boot_replay(argv[2]);
    memset(flash,0xff,sizeof(flash));
    ble_npl_eventq_init(&crypto_controller);
    ble_npl_event_init(&crypto_event,crypto_controller_callback,NULL);
    ble_npl_event_init(&crypto_host_event,crypto_host_callback,NULL);
    gopine_ble_set_firmware_version((const uint8_t*)"0.3.27",6);
    phone_device_information();
    ota_connection_tuning();
    ota_pipeline();
    ota_prepared_writes();
    mode_payload_lifecycle();
    mode_payload_budget();
    uuid_formatting();
    ota_ignores_suspended_notification_wakeups();
    uint16_t original_battery=0,original_music=0;
    int original_attrs=0,original_services=0,original_configs=0;
    for(unsigned i=0;i<100;i++) {
        assert(gopine_ble_start_update(80)==0);gopine_ble_poll();
        assert(synced && ble_gap_adv_active() && update_window);
        if(i==0) {
            original_battery=battery_handle;original_music=music_handle;
            original_attrs=ble_hs_max_attrs;original_services=ble_hs_max_services;
            original_configs=ble_hs_max_client_configs;
        }
        assert(battery_handle==original_battery && music_handle==original_music);
        assert(ble_hs_max_attrs==original_attrs && ble_hs_max_services==original_services &&
               ble_hs_max_client_configs==original_configs);
        connect_peer();assert(!phone_window && !phone_peer_find(1));
        discover();
        if(i==0)update_notifications();
        gopine_ble_stop();gopine_ble_poll();
        assert(!gopine_ble_busy() && connection==BLE_HS_CONN_HANDLE_NONE);
        assert_stopped_idle();
        if(i%10==0)phone_pairing_code(i==90);
    }
    phone_retry_advertising();
    phone_trace_large_mtu();
    phone_pairing_under_discovery();
    phone_pairing_policy();
    phone_complete_pairing(true,false);
    phone_complete_pairing(false,true);
    phone_complete_pairing(false,false);
    assert(crypto_yields>1000 && crypto_controller_runs==crypto_yields);
    assert(controller_resets>=120);
    assert(gopine_ble_alloc_peak()+2048<=gopine_ble_alloc_capacity());
    fprintf(stdout,"NimBLE arena: %zu-byte measured peak, %zu-byte capacity\n",
           gopine_ble_alloc_peak(),gopine_ble_alloc_capacity());
    puts("100 update cycles, full SC bonding, 8 private-address reconnects, controller-restore recovery and real GATT ANCS lifecycle passed");
}
