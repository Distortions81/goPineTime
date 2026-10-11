#ifndef GOPINE_UPDATE_MAILBOX_H
#define GOPINE_UPDATE_MAILBOX_H
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#define UPDATE_PACKET_WINDOW 2
struct update_packet { uint8_t data[244]; uint16_t size; uint8_t kind; };
struct update_mailbox {
    struct update_packet packets[UPDATE_PACKET_WINDOW];
    uint8_t head,count;
    bool in_flight;
};
static inline uint32_t update_packet_u32(const uint8_t *data) {
    return (uint32_t)data[0] | (uint32_t)data[1]<<8 |
           (uint32_t)data[2]<<16 | (uint32_t)data[3]<<24;
}
// The front packet keeps its slot while Go writes and reads back flash. A
// Take alone must not allow another packet into the advertised two-slot window.
static inline bool update_pending(const struct update_mailbox *m) {
    return m->count && !m->in_flight;
}
static inline int update_store(struct update_mailbox *m,const uint8_t status[16],
                               uint8_t kind,const uint8_t *data,unsigned n) {
    if((kind==1 && !((n==9 && data[0]==1) || (n==5 && (data[0]==2 || data[0]==3)))) ||
       (kind==2 && (n<9 || n>244)) || (kind!=1 && kind!=2)) return -1;
    if(m->count==UPDATE_PACKET_WINDOW)return 0;
    uint32_t total=update_packet_u32(status+4),offset=update_packet_u32(status+8);
    uint32_t session=update_packet_u32(status+12);
    if(status[0]!=2 || status[2])return -2;
    if(kind==1) {
        // Control operations cannot pass queued or uncommitted DATA.
        if(m->count)return 0;
        if(data[0]==1) {
            uint32_t requested=update_packet_u32(data+5);
            if(status[1]!=1 || total || offset || session || !update_packet_u32(data+1) ||
               requested<80 || requested>0x73000)return -2;
        } else {
            if(!session || !total || offset>total || update_packet_u32(data+1)!=session)
                return -2;
            if(data[0]==2 && (status[1]!=2 || offset!=total))return -2;
            if(data[0]==3 && (status[1]<2 || status[1]>4))return -2;
        }
    } else {
        if(status[1]!=2 || !session || !total || offset>total ||
           update_packet_u32(data)!=session)return -2;
        // Validate against durable bytes plus every outstanding packet, also
        // the packet already copied into Go but not acknowledged as durable.
        for(unsigned i=0;i<m->count;i++) {
            const struct update_packet *p=&m->packets[(m->head+i)%UPDATE_PACKET_WINDOW];
            if(p->kind!=2)return 0;
            if(update_packet_u32(p->data)!=session || update_packet_u32(p->data+4)!=offset ||
               p->size-8>total-offset)return -2;
            offset+=p->size-8;
        }
        if(update_packet_u32(data+4)!=offset || n-8>total-offset)return -2;
    }
    struct update_packet *p=&m->packets[(m->head+m->count)%UPDATE_PACKET_WINDOW];
    memcpy(p->data,data,n);p->size=n;p->kind=kind;m->count++;return 1;
}
static inline int update_take(struct update_mailbox *m,uint8_t *data,unsigned *n) {
    if(!update_pending(m)){*n=0;return 0;}
    const struct update_packet *p=&m->packets[m->head];
    *n=p->size;memcpy(data,p->data,*n);m->in_flight=true;return p->kind;
}
static inline void update_complete(struct update_mailbox *m,const uint8_t status[16]) {
    if(status[2] || status[1]==5) {memset(m,0,sizeof(*m));return;}
    if(!m->count || !m->in_flight)return;
    const struct update_packet *p=&m->packets[m->head];
    uint32_t session=update_packet_u32(status+12),offset=update_packet_u32(status+8);
    bool committed=false;
    if(p->kind==2) {
        committed=status[1]==2 && session==update_packet_u32(p->data) &&
            offset==update_packet_u32(p->data+4)+p->size-8;
    } else if(p->data[0]==1) {
        committed=status[1]==2 && session==update_packet_u32(p->data+1) && !offset &&
            update_packet_u32(status+4)==update_packet_u32(p->data+5);
    } else if(p->data[0]==2) {
        committed=(status[1]==3 || status[1]==4) && session==update_packet_u32(p->data+1);
    } else if(p->data[0]==3) {
        committed=session==update_packet_u32(p->data+1);
    }
    if(!committed)return;
    memset(&m->packets[m->head],0,sizeof(m->packets[m->head]));
    m->count--;m->head=(m->head+1)%UPDATE_PACKET_WINDOW;m->in_flight=false;
    if(!m->count)m->head=0;
}
#endif
