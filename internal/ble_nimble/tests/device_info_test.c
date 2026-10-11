#include "../port/device_info.h"
#include <assert.h>

static struct os_mbuf output;
static uint8_t result[48];
static unsigned result_size;
static bool no_buffer;
int os_mbuf_append(struct os_mbuf *om,const void *value,uint16_t size) {
    assert(om==&output && size<=sizeof(result));
    if(no_buffer)return -1;
    memcpy(result,value,size);result_size=size;return 0;
}
int main(void) {
    struct device_info info={0};
    struct ble_gatt_access_ctxt ctx={.op=BLE_GATT_ACCESS_OP_READ_CHR,.om=&output};
    assert(!device_info_read(&info,&ctx,0x2a26) && !result_size);
    const uint8_t version[]="0.3.27";
    assert(device_info_set_version(&info,version,sizeof(version)-1));
    assert(!device_info_read(&info,&ctx,0x2a26));
    assert(result_size==13 && !memcmp(result,"goPine 0.3.27",result_size));
    assert(!device_info_read(&info,&ctx,0x2a24));
    assert(result_size==8 && !memcmp(result,"PineTime",result_size));
    assert(!device_info_read(&info,&ctx,0x2a29));
    assert(result_size==6 && !memcmp(result,"PINE64",result_size));
    uint8_t oversized[48];memset(oversized,'x',sizeof(oversized));
    const uint8_t embedded_zero[]={'1',0,'2'},invalid_utf8[]={0xff};
    assert(!device_info_set_version(&info,NULL,1));
    assert(!device_info_set_version(&info,version,0));
    assert(!device_info_set_version(&info,oversized,sizeof(oversized)));
    assert(!device_info_set_version(&info,embedded_zero,sizeof(embedded_zero)));
    assert(!device_info_set_version(&info,invalid_utf8,sizeof(invalid_utf8)));
    assert(!strcmp(info.firmware,"goPine 0.3.27"));
    assert(device_info_set_version(&info,oversized,40));
    assert(info.firmware_length==47 && info.firmware[47]==0);
    assert(!device_info_set_version(&info,oversized,41));
    ctx.op=BLE_GATT_ACCESS_OP_WRITE_CHR;
    assert(device_info_read(&info,&ctx,0x2a26)==BLE_ATT_ERR_WRITE_NOT_PERMITTED);
    ctx.op=BLE_GATT_ACCESS_OP_READ_CHR;
    assert(device_info_read(&info,&ctx,0x2a25)==BLE_ATT_ERR_UNLIKELY);
    no_buffer=true;
    assert(device_info_read(&info,&ctx,0x2a26)==BLE_ATT_ERR_INSUFFICIENT_RES);
    return 0;
}
