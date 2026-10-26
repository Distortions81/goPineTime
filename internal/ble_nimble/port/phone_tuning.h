#ifndef GOPINE_PHONE_TUNING_H
#define GOPINE_PHONE_TUNING_H

#include "phone_security.h"

// Give encryption and initial service discovery 30 seconds at the central's
// original speed. Each new connection gets one optional request; rejection or
// an existing GAP procedure leaves it usable without repeated negotiations.
#define PHONE_TUNING_GRACE_MS 30000
struct phone_tuning {
    uint32_t at;
    bool attempted;
};

static inline uint32_t phone_tuning_delay(const struct phone_tuning *tuning,
                                         uint16_t handle,uint32_t now) {
    if(tuning->attempted || !phone_link_ready(handle))return UINT32_MAX;
    int32_t left=(int32_t)(tuning->at-now);
    return left>0?(uint32_t)left:0;
}

static inline void phone_tune(struct phone_tuning *tuning,uint16_t handle,uint32_t now) {
    if(phone_tuning_delay(tuning,handle,now))return;
    tuning->attempted=true;
    struct ble_gap_conn_desc desc;
    if(ble_gap_conn_find(handle,&desc))return;
    // Preserve a central's already economical timing (at least 150 ms between
    // mandatory events), including a longer interval with zero latency.
    if((uint32_t)desc.conn_itvl*(desc.conn_latency+1)>=120)return;
    // 30-45 ms base interval, up to four skipped events (150-225 ms idle
    // spacing), six-second supervision timeout. These fit Apple's published
    // interval/latency constraints. The central decides whether to accept.
    const struct ble_gap_upd_params params={
        .itvl_min=24,.itvl_max=36,.latency=4,.supervision_timeout=600
    };
    (void)ble_gap_update_params(handle,&params);
}

#endif
