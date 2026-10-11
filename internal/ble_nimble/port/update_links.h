#ifndef GOPINE_UPDATE_LINKS_H
#define GOPINE_UPDATE_LINKS_H

#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include "host/ble_hs.h"

// Two paired devices may automatically reconnect before the explicit updater.
#define UPDATE_LINK_COUNT 3
#define UPDATE_CANDIDATE_GRACE_MS 15000u
#define UPDATE_VERIFY_GRACE_MS 60000u

struct update_link {
    uint32_t deadline;
    uint16_t handle;
    bool subscribed, dirty, close_requested, terminating, verification_wait, tuning_attempted;
};
struct update_links {
    struct update_link entries[UPDATE_LINK_COUNT];
    uint16_t owner;
    bool advertising_pending;
};

static inline void update_links_reset(struct update_links *links) {
    memset(links,0,sizeof(*links));
    links->owner=BLE_HS_CONN_HANDLE_NONE;
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++)
        links->entries[i].handle=BLE_HS_CONN_HANDLE_NONE;
}
static inline struct update_link *update_links_find(struct update_links *links,uint16_t handle) {
    if(handle==BLE_HS_CONN_HANDLE_NONE)return NULL;
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++)
        if(links->entries[i].handle==handle)return &links->entries[i];
    return NULL;
}
static inline bool update_links_available(const struct update_links *links) {
    if(links->owner!=BLE_HS_CONN_HANDLE_NONE)return false;
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++)
        if(links->entries[i].handle==BLE_HS_CONN_HANDLE_NONE)return true;
    return false;
}
static inline bool update_links_add(struct update_links *links,uint16_t handle,uint32_t now) {
    if(update_links_find(links,handle))return false;
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++) {
        struct update_link *link=&links->entries[i];
        if(link->handle!=BLE_HS_CONN_HANDLE_NONE)continue;
        *link=(struct update_link){.handle=handle,.deadline=now+UPDATE_CANDIDATE_GRACE_MS};
        links->advertising_pending=true;
        return true;
    }
    return false;
}
static inline uint32_t update_links_delay(const struct update_links *links,uint32_t now) {
    if(links->advertising_pending)return 0;
    uint32_t delay=UINT32_MAX;
    for(unsigned i=0;i<UPDATE_LINK_COUNT;i++) {
        const struct update_link *link=&links->entries[i];
        if(link->handle==BLE_HS_CONN_HANDLE_NONE || link->terminating)continue;
        if(!link->close_requested && link->subscribed && link->dirty)return 0;
        if(!link->close_requested && link->handle==links->owner)continue;
        int32_t left=(int32_t)(link->deadline-now);
        uint32_t due=left>0?(uint32_t)left:0;
        if(due<delay)delay=due;
    }
    return delay;
}
static inline uint32_t update_u32(const uint8_t *data) {
    return (uint32_t)data[0] | (uint32_t)data[1]<<8 |
           (uint32_t)data[2]<<16 | (uint32_t)data[3]<<24;
}
static inline bool update_claimable(const uint8_t status[16],unsigned kind,
                                    const uint8_t *data,unsigned size) {
    if(status[2])return false;
    uint32_t total=update_u32(status+4),offset=update_u32(status+8),session=update_u32(status+12);
    if(kind==1 && size==9 && data[0]==1) {
        uint32_t requested=update_u32(data+5);
        // The receiver reserves the last 4KiB of the 0x74000 MCUboot slot.
        return status[1]==1 && !total && !offset && !session &&
               update_u32(data+1)!=0 && requested>=80 && requested<=0x73000;
    }
    if(status[1]!=2 || !session || !total || offset>total)return false;
    if(kind==2 && size>8 && size<=244)
        return update_u32(data)==session && update_u32(data+4)==offset && size-8<=total-offset;
    return kind==1 && size==5 && data[0]==2 && update_u32(data+1)==session && offset==total;
}

#endif
