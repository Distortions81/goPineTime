// iOS ANCS consumer, separate from the watch's ANS server. All procedures run
// cooperatively after the host callback unwinds; callbacks only advance state.
#include "ancs.h"

static const ble_uuid128_t service_uuid=BLE_UUID128_INIT(0xd0,0x00,0x2d,0x12,0x1e,0x4b,0x0f,0xa4,0x99,0x4e,0xce,0xb5,0x31,0xf4,0x05,0x79);
static const ble_uuid128_t source_uuid=BLE_UUID128_INIT(0xbd,0x1d,0xa2,0x99,0xe6,0x25,0x58,0x8c,0xd9,0x42,0x01,0x63,0x0d,0x12,0xbf,0x9f);
static const ble_uuid128_t control_uuid=BLE_UUID128_INIT(0xd9,0xd9,0xaa,0xfd,0xbd,0x9b,0x21,0x98,0xa8,0x49,0xe1,0x45,0xf3,0xd8,0xd1,0x69);
static const ble_uuid128_t data_uuid=BLE_UUID128_INIT(0xfb,0x7b,0x7c,0xce,0x6a,0xb3,0x44,0xbe,0xb5,0x4b,0xd6,0x24,0xe9,0xc6,0xea,0x22);
enum stage { IDLE, GATT_SERVICE, GATT_CHARS, GATT_DESC, GATT_SUB,
    ANCS_SERVICE, ANCS_CHARS, SOURCE_DESC, DATA_DESC, DATA_SUB, SOURCE_SUB, READY, WAIT_SERVICE, FAILED };
struct characteristic { uint16_t value,end,cccd; };
struct ancs_client {
    uint16_t conn,start,end,control;
    struct characteristic changed,source,data;
    enum stage stage;
    uint32_t epoch,deadline,retry_at;
    unsigned retries;
    bool busy,ready,restart,start_retry,trusted;
    uint8_t pending[4][8],pending_count;
    struct ancs_response response;
    struct ancs_mailbox mailbox;
};
static struct ancs_client clients[2] = {
    {.conn=BLE_HS_CONN_HANDLE_NONE}, {.conn=BLE_HS_CONN_HANDLE_NONE}
};
static uint32_t next_epoch;
static unsigned take_cursor;

static struct ancs_client *find_client(uint16_t conn) {
    if(conn==BLE_HS_CONN_HANDLE_NONE)return NULL;
    for(unsigned i=0;i<2;i++)if(clients[i].conn==conn)return &clients[i];
    return NULL;
}
static void reset_client(struct ancs_client *client,bool clear) {
    if(!++next_epoch)++next_epoch;
    memset(client,0,sizeof(*client));client->epoch=next_epoch;
    client->conn=BLE_HS_CONN_HANDLE_NONE;
    if(clear)ancs_clear(&client->mailbox);
}
static void *token(struct ancs_client *client) { return (void*)(uintptr_t)client->epoch; }
static struct ancs_client *current(uint16_t conn,void *arg) {
    struct ancs_client *client=find_client(conn);
    return client && client->trusted && (uintptr_t)arg==client->epoch?client:NULL;
}
static void fail(struct ancs_client *client) {
    client->busy=client->ready=client->start_retry=false;client->stage=FAILED;
    client->pending_count=0;memset(&client->response,0,sizeof(client->response));
}
void gopine_ancs_reset_peer(uint16_t conn) {
    struct ancs_client *client=find_client(conn);
    if(client)reset_client(client,true);
}
void gopine_ancs_reset(void) {
    for(unsigned i=0;i<2;i++) {
        struct ancs_client *client=&clients[i];
        bool clear=client->conn!=BLE_HS_CONN_HANDLE_NONE || client->mailbox.clear || client->mailbox.count;
        reset_client(client,clear);
    }
    take_cursor=0;
}
static void retry_service(struct ancs_client *client) {
    client->busy=false; client->stage=WAIT_SERVICE;
    client->retry_at=ble_npl_time_get()+30000;
}
static int service_cb(uint16_t conn,const struct ble_gatt_error *error,
                       const struct ble_gatt_svc *svc,void *arg) {
    struct ancs_client *client=current(conn,arg);
    if(!client)return 0;
    if(!error->status && svc) { client->start=svc->start_handle; client->end=svc->end_handle; return 0; }
    client->busy=false;
    if(error->status!=BLE_HS_EDONE) { fail(client); return 0; }
    if(client->stage==GATT_SERVICE) client->stage=client->start?GATT_CHARS:ANCS_SERVICE;
    else if(client->stage==ANCS_SERVICE) {
        if(client->start) client->stage=ANCS_CHARS; else retry_service(client);
    }
    return 0;
}
static void finish_characteristic(struct characteristic *c,uint16_t next) {
    if(c->value && !c->end && next>c->value) c->end=next-1;
}
static int chr_cb(uint16_t conn,const struct ble_gatt_error *error,
                   const struct ble_gatt_chr *chr,void *arg) {
    struct ancs_client *client=current(conn,arg);
    if(!client)return 0;
    if(!error->status && chr) {
        finish_characteristic(&client->changed,chr->def_handle);
        finish_characteristic(&client->source,chr->def_handle);
        finish_characteristic(&client->data,chr->def_handle);
        if(client->stage==GATT_CHARS) {
            if(!ble_uuid_cmp(&chr->uuid.u,BLE_UUID16_DECLARE(0x2a05)) && (chr->properties & BLE_GATT_CHR_PROP_INDICATE)) client->changed.value=chr->val_handle;
        } else {
            if(!ble_uuid_cmp(&chr->uuid.u,&source_uuid.u) && (chr->properties & BLE_GATT_CHR_PROP_NOTIFY)) client->source.value=chr->val_handle;
            if(!ble_uuid_cmp(&chr->uuid.u,&data_uuid.u) && (chr->properties & BLE_GATT_CHR_PROP_NOTIFY)) client->data.value=chr->val_handle;
            if(!ble_uuid_cmp(&chr->uuid.u,&control_uuid.u) && (chr->properties & BLE_GATT_CHR_PROP_WRITE)) client->control=chr->val_handle;
        }
        return 0;
    }
    client->busy=false;
    if(error->status!=BLE_HS_EDONE) { fail(client); return 0; }
    struct characteristic *chars[]={&client->changed,&client->source,&client->data};
    for(unsigned i=0;i<3;i++) if(chars[i]->value && !chars[i]->end) chars[i]->end=client->end;
    if(client->stage==GATT_CHARS) client->stage=client->changed.value?GATT_DESC:ANCS_SERVICE;
    else if(client->source.value && client->data.value && client->control) client->stage=SOURCE_DESC;
    else fail(client);
    return 0;
}
static struct characteristic *descriptor_target(struct ancs_client *client) {
    if(client->stage==GATT_DESC || client->stage==GATT_SUB) return &client->changed;
    if(client->stage==SOURCE_DESC || client->stage==SOURCE_SUB) return &client->source;
    return &client->data;
}
static int desc_cb(uint16_t conn,const struct ble_gatt_error *error,uint16_t value,
                    const struct ble_gatt_dsc *dsc,void *arg) {
    struct ancs_client *client=current(conn,arg);
    if(!client)return 0;
    struct characteristic *target=descriptor_target(client);
    if(!error->status && dsc) {
        if(value==target->value && !ble_uuid_cmp(&dsc->uuid.u,BLE_UUID16_DECLARE(0x2902))) target->cccd=dsc->handle;
        return 0;
    }
    client->busy=false;
    if(error->status!=BLE_HS_EDONE || !target->cccd) {
        if(client->stage==GATT_DESC) client->stage=ANCS_SERVICE; else fail(client);
        return 0;
    }
    switch(client->stage) {
    case GATT_DESC: client->stage=GATT_SUB; break;
    case SOURCE_DESC: client->stage=DATA_DESC; break;
    case DATA_DESC: client->stage=DATA_SUB; break;
    default: fail(client);
    }
    return 0;
}
static int write_cb(uint16_t conn,const struct ble_gatt_error *error,
                     struct ble_gatt_attr *attr,void *arg) {
    (void)attr;
    struct ancs_client *client=current(conn,arg);
    if(!client)return 0;
    client->busy=false;
    if(error->status) {
        if(client->stage==GATT_SUB) client->stage=ANCS_SERVICE;
        else if(!client->ready) retry_service(client);
        else if(error->status==BLE_HS_ATT_ERR(0xa2)) {
            // The notification disappeared before its attributes were read.
            // ANCS sends no data for a rejected command; move to the next UID.
            memset(&client->response,0,sizeof(client->response));
        }
        else fail(client); // Late attribute fragments must not be reused for a new UID.
        return 0;
    }
    switch(client->stage) {
    case GATT_SUB: client->stage=ANCS_SERVICE; break;
    case DATA_SUB: client->stage=SOURCE_SUB; break;
    case SOURCE_SUB: client->stage=READY; client->ready=true; break;
    case READY: break;
    default: fail(client);
    }
    return 0;
}
static bool due(uint32_t now,uint32_t deadline) { return (int32_t)(now-deadline)>=0; }
void gopine_ancs_poll(uint16_t conn,bool trusted) {
    if(conn==BLE_HS_CONN_HANDLE_NONE)return;
    struct ancs_client *client=find_client(conn);
    if(!trusted) {if(client)reset_client(client,true);return;}
    if(!client) {
        for(unsigned i=0;i<2;i++)if(clients[i].conn==BLE_HS_CONN_HANDLE_NONE) {client=&clients[i];break;}
        if(!client)return;
        reset_client(client,true);client->conn=conn;client->stage=GATT_SERVICE;client->trusted=true;
    }
    uint32_t now=ble_npl_time_get();
    if(client->restart && !client->busy) {
        reset_client(client,true);client->conn=conn;client->stage=GATT_SERVICE;client->trusted=true;
    }
    if(client->busy) { if(due(now,client->deadline)) fail(client); return; }
    if(client->response.active) { if(due(now,client->deadline)) fail(client); return; }
    if(client->stage==READY && !client->pending_count) { client->start_retry=false; return; }
    if(client->start_retry) {
        if(due(now,client->deadline)) { fail(client); return; }
        if(!due(now,client->retry_at)) return;
    }
    if(client->stage==WAIT_SERVICE) {
        if(client->retries>=3 || !due(now,client->retry_at)) return;
        client->retries++; client->stage=ANCS_SERVICE;
        memset(&client->source,0,sizeof(client->source)); memset(&client->data,0,sizeof(client->data)); client->control=0;
    }
    int rc=0;
    switch(client->stage) {
    case GATT_SERVICE: case ANCS_SERVICE:
        client->start=client->end=0;
        rc=ble_gattc_disc_svc_by_uuid(conn,client->stage==GATT_SERVICE?BLE_UUID16_DECLARE(0x1801):&service_uuid.u,service_cb,token(client)); break;
    case GATT_CHARS: case ANCS_CHARS:
        rc=ble_gattc_disc_all_chrs(conn,client->start,client->end,chr_cb,token(client)); break;
    case GATT_DESC: case SOURCE_DESC: case DATA_DESC: {
        struct characteristic *c=descriptor_target(client);
        if(c->value>=c->end) { if(client->stage==GATT_DESC) client->stage=ANCS_SERVICE; else fail(client); return; }
        rc=ble_gattc_disc_all_dscs(conn,c->value,c->end,desc_cb,token(client)); break;
    }
    case GATT_SUB: case DATA_SUB: case SOURCE_SUB: {
        const uint8_t enable[]={client->stage==GATT_SUB?2:1,0};
        rc=ble_gattc_write_flat(conn,descriptor_target(client)->cccd,enable,sizeof(enable),write_cb,token(client)); break;
    }
    case READY: {
        if(!client->pending_count) return;
        uint8_t source[8]; memcpy(source,client->pending[0],8);
        ancs_response_begin(&client->response,source);
        uint8_t request[]={0,source[4],source[5],source[6],source[7],1,40,0,3,100,0};
        rc=ble_gattc_write_flat(conn,client->control,request,sizeof(request),write_cb,token(client));
        if(!rc) memmove(client->pending,client->pending+1,(--client->pending_count)*8);
        else memset(&client->response,0,sizeof(client->response));
        break;
    }
    default: return;
    }
    if(rc==BLE_HS_ENOMEM) {
        // NimBLE leaves a failed initial start unqueued. Retry only this local
        // resource error: callbacks/timeouts may describe requests already sent.
        if(!client->start_retry) client->deadline=now+15000;
        client->start_retry=true; client->retry_at=now+1000; return;
    }
    if(rc) { fail(client); return; }
    client->start_retry=false;
    client->busy=true; client->deadline=now+15000;
}
void gopine_ancs_notify(uint16_t conn,uint16_t attr,struct os_mbuf *om) {
    struct ancs_client *client=find_client(conn);
    if(!client || !client->trusted || !om)return;
    unsigned n=OS_MBUF_PKTLEN(om);
    if(attr==client->changed.value && n==4) { client->restart=true; return; }
    if(attr==client->source.value && (client->stage==SOURCE_SUB || client->ready) && n==8) {
        uint8_t source[8]; if(os_mbuf_copydata(om,0,8,source)) return;
        if(source[0]>2 || source[2]>11) return;
        uint32_t uid=ancs_u32(source+4);
        for(unsigned i=0;i<client->pending_count;) {
            if(ancs_u32(client->pending[i]+4)==uid) {
                memmove(client->pending+i,client->pending+i+1,(--client->pending_count-i)*8);
            } else i++;
        }
        if(source[0]==2) {
            if(client->response.active && client->response.uid==uid) client->response.removed=true;
            uint8_t record[ANCS_RECORD_SIZE]={0}; memcpy(record,source+4,4); record[4]=2;
            ancs_store(&client->mailbox,record); return;
        }
        if(client->pending_count==4) { memmove(client->pending,client->pending+1,3*8); client->pending_count--; }
        memcpy(client->pending[client->pending_count++],source,8);
    } else if(attr==client->data.value && client->ready && client->response.active) {
        // Validate the entire ATT fragment before publishing a completed record;
        // an extra byte after its final tuple must not escape as a valid message.
        uint8_t bytes[ANCS_RESPONSE_SIZE];
        if(n>sizeof(bytes) || os_mbuf_copydata(om,0,n,bytes) ||
           ancs_response_feed(&client->response,&client->mailbox,bytes,n)<0) fail(client);
    }
}
int gopine_ancs_take(uint8_t *out,bool allowed) {
    for(unsigned n=0;n<2;n++) {
        unsigned index=(take_cursor+n)%2;
        struct ancs_client *client=&clients[index];
        if(!client->mailbox.clear && (!allowed || !client->trusted))continue;
        if(ancs_take(&client->mailbox,out)) {
            // Slot-local source IDs keep identical iPhone UIDs independent.
            // Reusing a slot always publishes its clear before new records.
            out[ANCS_RECORD_SIZE]=(uint8_t)(index+1);take_cursor=(index+1)%2;
            return ANCS_PACKET_SIZE;
        }
    }
    return 0;
}
static uint32_t client_delay(const struct ancs_client *client,uint32_t now) {
    if(client->busy || client->response.active)return due(now,client->deadline)?0:client->deadline-now;
    if(client->start_retry)return due(now,client->deadline) || due(now,client->retry_at)?0:client->retry_at-now;
    if(client->stage==WAIT_SERVICE)return client->retries>=3?240000:(due(now,client->retry_at)?0:client->retry_at-now);
    if(client->stage==FAILED || client->stage==IDLE || (client->stage==READY && !client->pending_count && !client->restart))return 240000;
    return 0;
}
bool gopine_ancs_updates(void) {
    for(unsigned i=0;i<2;i++)if(clients[i].mailbox.clear || clients[i].mailbox.count)return true;
    return gopine_ancs_delay()==0;
}
uint32_t gopine_ancs_delay(void) {
    uint32_t now=ble_npl_time_get(),delay=240000;
    for(unsigned i=0;i<2;i++) {
        uint32_t next=client_delay(&clients[i],now);
        if(next<delay)delay=next;
    }
    return delay;
}
