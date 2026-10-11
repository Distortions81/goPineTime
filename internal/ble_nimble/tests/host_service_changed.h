// Replay a real synthetic bond in a new process, so the GATT registration and
// firmware-boot cache invalidation are exercised with no surviving RAM state.
// No Bluetooth hardware or real pairing keys are used by this fixture.
#include <fcntl.h>
#include <sys/wait.h>
#include <unistd.h>

static uint16_t service_changed_handle(void) {
    uint16_t handle;
    assert(!ble_gatts_find_chr(BLE_UUID16_DECLARE(0x1801),BLE_UUID16_DECLARE(0x2a05),NULL,&handle));
    return handle;
}
static void service_changed_subscribe(void) {
    uint16_t cccd;
    assert(!ble_gatts_find_dsc(BLE_UUID16_DECLARE(0x1801),BLE_UUID16_DECLARE(0x2a05),
                              BLE_UUID16_DECLARE(0x2902),&cccd));
    uint8_t write[]={BLE_ATT_OP_WRITE_REQ,0,0,2,0};put_le16(write+1,cccd);
    att(write,sizeof(write),BLE_ATT_OP_WRITE_RSP);
    struct ble_gap_conn_desc desc;assert(!ble_gap_conn_find(1,&desc));
    struct ble_store_key_cccd key={.peer_addr=desc.peer_id_addr,.chr_val_handle=cccd-1};
    struct ble_store_value_cccd value;
    assert(!ble_store_read_cccd(&key,&value));
}
static void service_changed_pending(const ble_addr_t *identity,bool pending) {
    struct ble_store_key_cccd key={.peer_addr=*identity,.chr_val_handle=service_changed_handle()};
    struct ble_store_value_cccd value;
    int rc=ble_store_read_cccd(&key,&value);
    if(rc)fprintf(stderr,"Service Changed CCCD missing: handle %u pending %u status %d\n",
                   key.chr_val_handle,pending,rc);
    assert(!rc);
    assert(value.flags==BLE_GATTS_CLT_CFG_F_INDICATE && !!value.value_changed==pending);
}
static unsigned service_changed_indications(void) {
    unsigned count=0;
    for(unsigned offset=0;offset<reply_len;) {
        assert(offset+8<=reply_len);
        unsigned size=get_le16(reply+offset+4);
        assert(offset+8+size<=reply_len);
        const uint8_t *pdu=reply+offset+8;
        if(get_le16(reply+offset+6)==BLE_L2CAP_CID_ATT && size>=3 &&
           pdu[0]==BLE_ATT_OP_INDICATE_REQ && get_le16(pdu+1)==service_changed_handle()) {
            assert(size==7 && get_le16(pdu+3)==1 && get_le16(pdu+5)==0xffff);
            count++;
        }
        offset+=8+size;
    }
    return count;
}
static void service_changed_keys_unchanged(const struct ble_store_value_sec *our,
                                          const struct ble_store_value_sec *peer) {
    struct ble_store_key_sec key={.peer_addr=our->peer_addr};
    struct ble_store_value_sec value;
    assert(!ble_store_read_our_sec(&key,&value) && !memcmp(our,&value,sizeof(value)));
    assert(!ble_store_read_peer_sec(&key,&value) && !memcmp(peer,&value,sizeof(value)));
}
static int service_changed_boot_replay(const char *descriptor) {
    FILE *fixture=fdopen(atoi(descriptor),"rb");assert(fixture);
    struct ble_store_value_sec our,peer;
    assert(fread(flash,sizeof(flash),1,fixture)==1);
    assert(fread(&our,sizeof(our),1,fixture)==1);
    assert(fread(&peer,sizeof(peer),1,fixture)==1);
    assert(!fclose(fixture));
    gopine_ble_set_firmware_version((const uint8_t*)"0.3.27",6);

    // Starting in OTA still registers the new database and marks the saved
    // subscriber pending. An unrelated OTA central must not consume it.
    assert(!gopine_ble_start_update(80));gopine_ble_poll();
    service_changed_keys_unchanged(&our,&peer);
    service_changed_pending(&our.peer_addr,true);
    const uint8_t unrelated[]={6,5,4,3,2,0xc1};memcpy(peer_address,unrelated,6);
    connect_peer();assert(!phone_link_ready(1));
    assert(!service_changed_indications());
    uint8_t read[]={BLE_ATT_OP_READ_REQ,0,0};put_le16(read+1,battery_handle);
    att(read,sizeof(read),BLE_ATT_OP_READ_RSP);assert(!service_changed_indications());
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2);

    // Pending state survives the actual durable journal, not just the in-RAM
    // CCCD. The saved phone then restores its existing LTK without re-pairing.
    gopine_bond_init();service_changed_pending(&our.peer_addr,true);
    memcpy(peer_address,our.peer_addr.val,6);
    uint8_t time[10]={0};assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    connect_peer();assert(!phone_link_ready(1) && !pairing_code);
    assert(!service_changed_indications());
    request_encryption(our.ltk);assert_authenticated();
    assert(service_changed_indications()==1);
    service_changed_pending(&our.peer_addr,true);
    uint8_t ack[]={BLE_ATT_OP_INDICATE_RSP};receive_l2cap(BLE_L2CAP_CID_ATT,ack,sizeof(ack));
    service_changed_pending(&our.peer_addr,false);
    service_changed_keys_unchanged(&our,&peer);
    // The invalidated central can now discover the appended DIS and retrieve
    // the actual new firmware value, rather than remaining on its old cache.
    uint8_t discover_dis[]={BLE_ATT_OP_FIND_TYPE_VALUE_REQ,1,0,255,255,0,0x28,0x0a,0x18};
    att(discover_dis,sizeof(discover_dis),BLE_ATT_OP_FIND_TYPE_VALUE_RSP);
    uint16_t dis_start=get_le16(reply+9),dis_end=get_le16(reply+11);
    uint8_t discover_firmware[]={BLE_ATT_OP_READ_TYPE_REQ,0,0,0,0,0x26,0x2a};
    put_le16(discover_firmware+1,dis_start);put_le16(discover_firmware+3,dis_end);
    att(discover_firmware,sizeof(discover_firmware),BLE_ATT_OP_READ_TYPE_RSP);
    assert(reply_len==25 && reply[9]==15 && !memcmp(reply+12,"goPine 0.3.27",13));
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2);

    // A mode / host restart is not a new firmware boot: ACK stays durable and
    // this database must not cause another rediscovery or another flash write.
    unsigned saved_writes=flash_writes;
    gopine_bond_init();service_changed_pending(&our.peer_addr,false);
    assert(!gopine_ble_start_phone(time,80,0));gopine_ble_poll();
    service_changed_pending(&our.peer_addr,false);
    connect_peer();request_encryption(our.ltk);assert_authenticated();
    assert(!service_changed_indications());
    service_changed_keys_unchanged(&our,&peer);
    gopine_ble_stop();gopine_ble_poll();gopine_ble_poll();
    assert(!gopine_ble_busy() && gopine_bond_status()==2 && flash_writes==saved_writes);
    puts("cold-boot Service Changed: persisted subscription, OTA isolation, encrypted indication and ACK passed");
    return 0;
}
static void service_changed_reboot_fixture(void) {
    struct ble_store_key_sec key={.peer_addr=*BLE_ADDR_ANY};
    struct ble_store_value_sec our,peer;
    assert(!ble_store_read_our_sec(&key,&our));
    key.peer_addr=our.peer_addr;assert(!ble_store_read_peer_sec(&key,&peer));
    service_changed_pending(&our.peer_addr,false);
    assert(!gopine_ble_busy() && gopine_bond_status()==2);
    FILE *fixture=tmpfile();assert(fixture);
    assert(fwrite(flash,sizeof(flash),1,fixture)==1);
    assert(fwrite(&our,sizeof(our),1,fixture)==1);
    assert(fwrite(&peer,sizeof(peer),1,fixture)==1);
    assert(!fflush(fixture));rewind(fixture);
    int fd=fileno(fixture);assert(fcntl(fd,F_SETFD,0)==0);
    char descriptor[24];snprintf(descriptor,sizeof(descriptor),"%d",fd);
    pid_t child=fork();assert(child>=0);
    if(!child) {
        execl("/proc/self/exe","host","--service-changed-reboot",descriptor,(char*)NULL);
        _exit(127);
    }
    int status;assert(waitpid(child,&status,0)==child);
    assert(WIFEXITED(status) && WEXITSTATUS(status)==0);
    assert(!fclose(fixture));
}
