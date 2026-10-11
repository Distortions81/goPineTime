// A peer ANCS server exercised over ATT/HCI with the real pinned GATT client.
// UUIDs are Apple's published values; no production ANCS state is inspected.
static const uint8_t apple_ancs_uuid[]={0xd0,0,0x2d,0x12,0x1e,0x4b,0x0f,0xa4,0x99,0x4e,0xce,0xb5,0x31,0xf4,5,0x79};
static const uint8_t apple_source_uuid[]={0xbd,0x1d,0xa2,0x99,0xe6,0x25,0x58,0x8c,0xd9,0x42,1,0x63,0x0d,0x12,0xbf,0x9f};
static const uint8_t apple_control_uuid[]={0xd9,0xd9,0xaa,0xfd,0xbd,0x9b,0x21,0x98,0xa8,0x49,0xe1,0x45,0xf3,0xd8,0xd1,0x69};
static const uint8_t apple_data_uuid[]={0xfb,0x7b,0x7c,0xce,0x6a,0xb3,0x44,0xbe,0xb5,0x4b,0xd6,0x24,0xe9,0xc6,0xea,0x22};
struct apple_server {
    bool available,data_subscribed,source_subscribed,deny_subscription;
    unsigned attributes,queries;
};
static struct apple_server apple_servers[2];
static uint16_t apple_connection=1;
static bool apple_hold_attributes;
static struct client_request apple_held_attributes[2];
static unsigned apple_held_count;
#define apple_service_available (apple_servers[apple_connection-1].available)
#define apple_data_subscribed (apple_servers[apple_connection-1].data_subscribed)
#define apple_source_subscribed (apple_servers[apple_connection-1].source_subscribed)
#define apple_deny_subscription (apple_servers[apple_connection-1].deny_subscription)
#define apple_attributes (apple_servers[apple_connection-1].attributes)
#define apple_service_queries (apple_servers[apple_connection-1].queries)

static void apple_att(const uint8_t *data,unsigned size) {
    receive_l2cap_on(apple_connection,BLE_L2CAP_CID_ATT,data,size);
}
static void apple_notify(uint16_t handle,const uint8_t *data,unsigned size,bool indicate) {
    uint8_t packet[32]={indicate?BLE_ATT_OP_INDICATE_REQ:BLE_ATT_OP_NOTIFY_REQ};
    assert(size<=sizeof(packet)-3);put_le16(packet+1,handle);memcpy(packet+3,data,size);
    apple_att(packet,size+3);
}
static void apple_source(uint32_t uid,uint8_t event) {
    uint8_t data[]={event,4,4,1,0,0,0,0};put_le32(data+4,uid);
    apple_notify(22,data,sizeof(data),false);
}
static void apple_error(const struct client_request *request,uint8_t error) {
    uint8_t response[]={BLE_ATT_OP_ERROR_RSP,request->data[0],request->data[1],request->data[2],error};
    apple_att(response,sizeof(response));
}
static void apple_answer(const struct client_request *request) {
    assert(request->connection>=1 && request->connection<=2);
    apple_connection=request->connection;
    const uint8_t *data=request->data;
    uint16_t start=get_le16(data+1);
    switch(data[0]) {
    case BLE_ATT_OP_FIND_TYPE_VALUE_REQ: {
        assert(get_le16(data+5)==0x2800);
        bool gatt=request->size==9 && get_le16(data+7)==0x1801;
        bool ancs=request->size==23 && !memcmp(data+7,apple_ancs_uuid,16);
        assert(gatt || ancs);if(ancs)apple_service_queries++;
        uint16_t svc=gatt?1:20,end=gatt?9:40;
        if(start>svc || (ancs && !apple_service_available)) {
            apple_error(request,BLE_ATT_ERR_ATTR_NOT_FOUND);return;
        }
        uint8_t response[]={BLE_ATT_OP_FIND_TYPE_VALUE_RSP,0,0,0,0};
        put_le16(response+1,svc);put_le16(response+3,end);apple_att(response,sizeof(response));return;
    }
    case BLE_ATT_OP_READ_TYPE_REQ: {
        assert(request->size==7 && get_le16(data+5)==0x2803);
        uint8_t response[23]={BLE_ATT_OP_READ_TYPE_RSP,21};
        uint16_t handle;
        if(start<10) {
            if(start>2) {apple_error(request,BLE_ATT_ERR_ATTR_NOT_FOUND);return;}
            response[1]=7;put_le16(response+2,2);response[4]=BLE_GATT_CHR_PROP_INDICATE;
            put_le16(response+5,3);put_le16(response+7,0x2a05);apple_att(response,9);return;
        }
        const uint8_t *uuid;uint8_t properties;
        if(start<=21) {handle=21;uuid=apple_source_uuid;properties=BLE_GATT_CHR_PROP_NOTIFY;}
        else if(start<=26) {handle=26;uuid=apple_control_uuid;properties=BLE_GATT_CHR_PROP_WRITE;}
        else if(start<=30) {handle=30;uuid=apple_data_uuid;properties=BLE_GATT_CHR_PROP_NOTIFY;}
        else {apple_error(request,BLE_ATT_ERR_ATTR_NOT_FOUND);return;}
        put_le16(response+2,handle);response[4]=properties;put_le16(response+5,handle+1);
        memcpy(response+7,uuid,16);apple_att(response,sizeof(response));return;
    }
    case BLE_ATT_OP_FIND_INFO_REQ: {
        assert(request->size==5);
        uint16_t end=get_le16(data+3),cccd=start<=4?4:start<=23?23:32;
        if(start>cccd || cccd>end) {apple_error(request,BLE_ATT_ERR_ATTR_NOT_FOUND);return;}
        uint8_t response[]={BLE_ATT_OP_FIND_INFO_RSP,1,0,0,2,0x29};
        put_le16(response+2,cccd);apple_att(response,sizeof(response));return;
    }
    case BLE_ATT_OP_WRITE_REQ: {
        if(start==4 || start==23 || start==32) {
            assert(request->size==5 && get_le16(data+3)==(start==4?2:1));
            if(start==32 && apple_deny_subscription) {
                apple_error(request,BLE_ATT_ERR_INSUFFICIENT_AUTHOR);return;
            }
            if(start==32)apple_data_subscribed=true;
            if(start==23) {
                assert(apple_data_subscribed);apple_source_subscribed=true;
                // iOS may send existing messages before the CCCD write reply.
                apple_source(0x12345678,0);
            }
            const uint8_t response=BLE_ATT_OP_WRITE_RSP;apple_att(&response,1);return;
        }
        assert(start==27 && request->size==14 && apple_source_subscribed && apple_data_subscribed);
        assert(data[3]==0 && data[8]==1 && get_le16(data+9)==40 &&
               data[11]==3 && get_le16(data+12)==100);
        apple_attributes++;
        if(apple_hold_attributes) {
            assert(apple_held_count<2);apple_held_attributes[apple_held_count++]=*request;return;
        }
        const uint8_t response=BLE_ATT_OP_WRITE_RSP;apple_att(&response,1);
        uint8_t attributes[151]={0};memcpy(attributes+1,data+4,4);
        attributes[5]=1;put_le16(attributes+6,40);memset(attributes+8,'T',40);
        attributes[48]=3;put_le16(attributes+49,100);memset(attributes+51,'B',100);
        // Split the full-size result over real ATT notifications, including
        // tuple/header boundaries, rather than injecting parser callbacks.
        for(unsigned offset=0;offset<sizeof(attributes);) {
            unsigned n=sizeof(attributes)-offset;if(n>17)n=17;
            apple_notify(31,attributes+offset,n,false);offset+=n;
        }
        return;
    }
    default: assert(false);
    }
}
static void apple_drain(void) {
    for(unsigned iterations=0;client_request_count;iterations++) {
        assert(iterations<40);
        struct client_request request=client_requests[0];
        memmove(client_requests,client_requests+1,--client_request_count*sizeof(*client_requests));
        apple_answer(&request);
    }
}
static void phone_ancs_traffic(bool service_changed,bool authorization_retry) {
    apple_connection=1;retain_apple=true;apple_service_available=true;
    apple_data_subscribed=apple_source_subscribed=false;
    apple_deny_subscription=authorization_retry;
    unsigned before=apple_attributes;
    assert(client_request_count);apple_drain();
    uint8_t out[ANCS_PACKET_SIZE];
    if(authorization_retry) {
        assert(!apple_data_subscribed && !apple_source_subscribed && apple_attributes==before);
        int n=gopine_ble_take_apple_notification(out);
        assert(!n || out[4]==3);assert(!gopine_ble_take_apple_notification(out));
        // Notification sharing denial must not close the bond or block other
        // phone features. Allow sharing before the bounded retry deadline.
        assert_authenticated();
        uint8_t weather[49]={0,0};weather[10]=18;
        write_characteristic(&weather_uuid.u,&weather_data_uuid.u,weather,sizeof(weather),BLE_ATT_OP_WRITE_RSP);
        assert(gopine_ble_take_weather(out)==49 && out[10]==18);
        assert(connection==1 && !phone_link_permission(1));
        apple_deny_subscription=false;
        test_rtc0.COUNTER+=31*32768;gopine_ble_poll();apple_drain();
    }
    assert(apple_source_subscribed && apple_data_subscribed && apple_attributes==before+1);
    // Setup/connection clear may precede the first notification.
    int n=gopine_ble_take_apple_notification(out);
    if(n && out[4]==3)n=gopine_ble_take_apple_notification(out);
    assert(n==ANCS_PACKET_SIZE && get_le32(out)==0x12345678 && out[4]==0 && out[6]==4);
    for(unsigned i=7;i<47;i++)assert(out[i]=='T');
    for(unsigned i=47;i<147;i++)assert(out[i]=='B');
    assert(!gopine_ble_take_apple_notification(out));
    apple_source(0x12345678,2);
    assert(gopine_ble_take_apple_notification(out)==ANCS_PACKET_SIZE && out[4]==2);
    assert(!client_request_count); // Removal never requests expired attributes.
    if(service_changed) {
        unsigned confirmations=indication_confirmations,queries=apple_service_queries;
        apple_service_available=false;
        const uint8_t changed[]={20,0,40,0};apple_notify(3,changed,4,true);apple_drain();
        assert(indication_confirmations==confirmations+1 && apple_service_queries>queries);
        assert(gopine_ble_take_apple_notification(out)==ANCS_PACKET_SIZE && out[4]==3);
        assert(!gopine_ble_take_apple_notification(out));
        // A later publish indication must restart discovery on this same bond.
        apple_service_available=true;apple_data_subscribed=apple_source_subscribed=false;
        apple_notify(3,changed,4,true);apple_drain();
        assert(indication_confirmations==confirmations+2 && apple_attributes==before+2);
        n=gopine_ble_take_apple_notification(out);
        if(n && out[4]==3)n=gopine_ble_take_apple_notification(out);
        assert(n==ANCS_PACKET_SIZE && out[4]==0 && get_le32(out)==0x12345678);
    }
    assert(connection==1 && !phone_link_permission(1) && !client_request_count);
    retain_apple=false;
}
