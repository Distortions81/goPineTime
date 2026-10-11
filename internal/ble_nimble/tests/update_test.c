#include "../port/update_mailbox.h"
#include "../port/battery_security.h"
#include <assert.h>

static struct os_mbuf output;
static unsigned appended;
static uint8_t battery_byte;
static bool no_buffer;

int os_mbuf_append(struct os_mbuf *om,const void *data,uint16_t size) {
    assert(om==&output && size==1);
    if(no_buffer) return -1;
    appended++;
    battery_byte=*(const uint8_t*)data;
    return 0;
}
static void battery_public_read(void) {
    // Reference Battery READ has no connection or key lookup and works before
    // encryption, during OTA discovery, and through NimBLE's local notify read.
    assert(GOPINE_BATTERY_FLAGS==(BLE_GATT_CHR_F_READ | BLE_GATT_CHR_F_NOTIFY));
    struct ble_gatt_access_ctxt ctx={.op=BLE_GATT_ACCESS_OP_READ_CHR,.om=&output};
    assert(!battery_read_access(&ctx,79));
    assert(battery_byte==79 && appended==1);
    assert(!battery_read_access(&ctx,0));
    assert(battery_byte==0 && appended==2);
    assert(!battery_read_access(&ctx,100));
    assert(battery_byte==100 && appended==3);
    ctx.op=BLE_GATT_ACCESS_OP_WRITE_CHR;
    assert(battery_read_access(&ctx,79)==BLE_ATT_ERR_UNLIKELY);
    assert(appended==3);
    ctx.op=BLE_GATT_ACCESS_OP_READ_CHR;
    no_buffer=true;
    assert(battery_read_access(&ctx,79)==BLE_ATT_ERR_INSUFFICIENT_RES);
}

static void put_u32(uint8_t *data,uint32_t value) {
    for(unsigned i=0;i<4;i++)data[i]=(uint8_t)(value>>(8*i));
}
static void status_set(uint8_t status[16],uint8_t phase,uint32_t total,uint32_t offset) {
    memset(status,0,16);status[0]=2;status[1]=phase;
    put_u32(status+4,total);put_u32(status+8,offset);put_u32(status+12,42);
}
static void packet_set(uint8_t data[245],uint32_t offset,uint8_t value) {
    memset(data,value,245);put_u32(data,42);put_u32(data+4,offset);
}
static void reject_unchanged(struct update_mailbox *m,const uint8_t status[16],
                              unsigned kind,const uint8_t *data,unsigned size,int result) {
    struct update_mailbox before=*m;
    assert(update_store(m,status,kind,data,size)==result);
    assert(!memcmp(m,&before,sizeof(*m)));
}
static void pipeline(void) {
    struct update_mailbox m={0};uint8_t data[245]={1},out[244],status[16]={2,1};unsigned size;
    assert(sizeof(m)<=500); // Two packets fit the measured firmware RAM budget.
    for(unsigned n=0;n<9;n++)reject_unchanged(&m,status,1,data,n,-1);
    reject_unchanged(&m,status,2,data,245,-1);
    reject_unchanged(&m,status,2,data,8,-1);
    reject_unchanged(&m,status,3,data,9,-1);
    put_u32(data+1,42);put_u32(data+5,79);
    reject_unchanged(&m,status,1,data,9,-2);
    put_u32(data+5,0x73001);reject_unchanged(&m,status,1,data,9,-2);
    put_u32(data+5,472);put_u32(data+1,0);reject_unchanged(&m,status,1,data,9,-2);
    put_u32(data+1,42);assert(update_store(&m,status,1,data,9)==1);
    assert(update_take(&m,out,&size)==1 && size==9 && !memcmp(out,data,size));
    assert(m.count==1 && m.in_flight && !update_pending(&m));
    assert(update_take(&m,out,&size)==0 && size==0);
    update_complete(&m,status);assert(m.count==1); // The old status is not an ACK.
    packet_set(data,0,0x5a);reject_unchanged(&m,status,2,data,244,-2);
    status_set(status,2,472,0);update_complete(&m,status);assert(!m.count);

    packet_set(data,0,0x5a);assert(update_store(&m,status,2,data,244)==1);
    reject_unchanged(&m,status,2,data,244,-2); // Duplicate, still in the queue.
    put_u32(data+4,237);reject_unchanged(&m,status,2,data,244,-2); // Gap.
    put_u32(data+4,235);reject_unchanged(&m,status,2,data,244,-2); // Overlap.
    put_u32(data+4,236);put_u32(data,43);reject_unchanged(&m,status,2,data,244,-2);
    packet_set(data,236,0xa5);assert(update_store(&m,status,2,data,244)==1);
    packet_set(data,472,0x33);reject_unchanged(&m,status,2,data,9,0);
    uint8_t verify[]={2,42,0,0,0};reject_unchanged(&m,status,1,verify,5,0);
    assert(update_take(&m,out,&size)==2 && size==244 && out[8]==0x5a);
    assert(m.count==2 && m.in_flight && !update_pending(&m));
    reject_unchanged(&m,status,2,data,9,0); // Taking does not free the slot.
    put_u32(status+8,235);update_complete(&m,status);assert(m.count==2);
    put_u32(status+8,236);put_u32(status+12,43);update_complete(&m,status);assert(m.count==2);
    put_u32(status+12,42);update_complete(&m,status);
    assert(m.count==1 && update_pending(&m));
    reject_unchanged(&m,status,2,data,9,-2); // Beyond the image, even with one free slot.
    reject_unchanged(&m,status,1,verify,5,0);
    assert(update_take(&m,out,&size)==2 && size==244 && out[8]==0xa5);
    put_u32(status+8,472);update_complete(&m,status);assert(!m.count && !m.in_flight);
    packet_set(data,0,0x5a);reject_unchanged(&m,status,2,data,244,-2); // Durable duplicate.
    assert(update_store(&m,status,1,verify,5)==1);
    assert(update_take(&m,out,&size)==1 && size==5);
    update_complete(&m,status);assert(m.count==1);
    status[1]=3;update_complete(&m,status);assert(!m.count);
    reject_unchanged(&m,status,2,data,244,-2);
}
static void failure_resume_and_wrap(void) {
    struct update_mailbox m={0};uint8_t status[16],data[245],out[244];unsigned size;
    status_set(status,2,20000,0);
    for(unsigned offset=0;offset<10000;offset++) {
        packet_set(data,offset,offset);
        assert(update_store(&m,status,2,data,9)==1);
        assert(update_take(&m,out,&size)==2 && size==9 && out[8]==(uint8_t)offset);
        put_u32(status+8,offset+1);update_complete(&m,status);
        assert(!m.count);
    }
    packet_set(data,10000,0x11);assert(update_store(&m,status,2,data,244)==1);
    packet_set(data,10236,0x22);assert(update_store(&m,status,2,data,244)==1);
    assert(update_take(&m,out,&size)==2);
    status[1]=5;status[2]=1;update_complete(&m,status);
    assert(!m.count && !m.in_flight && update_take(&m,out,&size)==0);
    reject_unchanged(&m,status,2,data,244,-2);

    status_set(status,2,20000,10000);
    packet_set(data,10000,0x11);assert(update_store(&m,status,2,data,244)==1);
    packet_set(data,10236,0x22);assert(update_store(&m,status,2,data,244)==1);
    assert(update_take(&m,out,&size)==2);
    put_u32(status+8,10236);update_complete(&m,status);
    packet_set(data,10472,0x33);assert(update_store(&m,status,2,data,244)==1); // Wrap tail.
    assert(update_take(&m,out,&size)==2 && out[8]==0x22);
    // Owner disconnect discards the in-flight packet and queued tail. The new
    // owner may only resume from the published, flash-verified offset.
    memset(&m,0,sizeof(m));
    reject_unchanged(&m,status,2,data,244,-2);
    packet_set(data,10236,0x44);assert(update_store(&m,status,2,data,244)==1);
    assert(update_take(&m,out,&size)==2 && out[8]==0x44);
    put_u32(status+8,10472);update_complete(&m,status);
    uint8_t verify[]={2,42,0,0,0};reject_unchanged(&m,status,1,verify,5,-2);
    uint8_t cancel[]={3,42,0,0,0};assert(update_store(&m,status,1,cancel,5)==1);
    assert(update_take(&m,out,&size)==1);update_complete(&m,status);assert(!m.count);
}
int main(void) {
    battery_public_read();pipeline();failure_resume_and_wrap();
    return 0;
}
