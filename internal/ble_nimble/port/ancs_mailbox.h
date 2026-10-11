#ifndef GOPINE_ANCS_MAILBOX_H
#define GOPINE_ANCS_MAILBOX_H
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

// Internal record: UID (LE32), event, category, flags, title[40], body[100].
// Event 3 clears this ANCS session. Never interpret these as legacy ANS packets.
#define ANCS_RECORD_SIZE 147
#define ANCS_RESPONSE_SIZE 151
struct ancs_mailbox {
    uint8_t records[2][ANCS_RECORD_SIZE], head, count;
    bool clear;
};
struct ancs_response {
    uint8_t data[ANCS_RESPONSE_SIZE], event, category, flags;
    uint32_t uid;
    unsigned size;
    bool active, removed;
};
static inline uint32_t ancs_u32(const uint8_t *p) {
    return (uint32_t)p[0] | (uint32_t)p[1]<<8 | (uint32_t)p[2]<<16 | (uint32_t)p[3]<<24;
}
static inline unsigned ancs_u16(const uint8_t *p) { return p[0] | (unsigned)p[1]<<8; }
// Reserve only after a complete response validates. Storage belongs to this
// peer's mailbox until Go takes it; no transient full record is needed.
static inline uint8_t *ancs_reserve(struct ancs_mailbox *m) {
    if(m->count==2) { m->head=(m->head+1)%2; m->count--; }
    uint8_t *record=m->records[(m->head+m->count)%2];
    m->count++; return record;
}
static inline void ancs_store(struct ancs_mailbox *m, const uint8_t *record) {
    memcpy(ancs_reserve(m),record,ANCS_RECORD_SIZE);
}
static inline void ancs_clear(struct ancs_mailbox *m) {
    memset(m,0,sizeof(*m)); m->clear=true;
}
static inline int ancs_take(struct ancs_mailbox *m,uint8_t *out) {
    if(m->clear) { memset(out,0,ANCS_RECORD_SIZE); out[4]=3; m->clear=false; return ANCS_RECORD_SIZE; }
    if(!m->count) return 0;
    memcpy(out,m->records[m->head],ANCS_RECORD_SIZE);
    memset(m->records[m->head],0,ANCS_RECORD_SIZE);
    m->head=(m->head+1)%2; m->count--;
    return ANCS_RECORD_SIZE;
}
static inline void ancs_response_begin(struct ancs_response *r,const uint8_t *source) {
    memset(r,0,sizeof(*r));
    r->uid=ancs_u32(source+4); r->event=source[0]; r->category=source[2]; r->flags=source[1]; r->active=true;
}
// Fragment boundaries can split any header/length/text byte. Only the two
// requested attributes in their specified order are accepted. A malformed
// stream must close the session rather than contaminate a later request.
static inline int ancs_response_feed(struct ancs_response *r,struct ancs_mailbox *m,
                                      const uint8_t *data,unsigned size) {
    if(!r->active || size>sizeof(r->data)-r->size) return -1;
    memcpy(r->data+r->size,data,size); r->size+=size;
    if(r->size<5) return 0;
    if(r->data[0]!=0 || ancs_u32(r->data+1)!=r->uid) return -1;
    if(r->size<8) return 0;
    unsigned title=ancs_u16(r->data+6);
    if(r->data[5]!=1 || title>40) return -1;
    unsigned at=8+title;
    if(r->size<at+3) return 0;
    unsigned body=ancs_u16(r->data+at+1);
    if(r->data[at]!=3 || body>100) return -1;
    unsigned end=at+3+body;
    if(r->size<end) return 0;
    if(r->size!=end) return -1;
    if(!r->removed) {
        uint8_t *record=ancs_reserve(m);
        memset(record,0,ANCS_RECORD_SIZE);
        memcpy(record,r->data+1,4); record[4]=r->event; record[5]=r->category; record[6]=r->flags;
        memcpy(record+7,r->data+8,title); memcpy(record+47,r->data+at+3,body);
    }
    memset(r,0,sizeof(*r)); return 1;
}
#endif
