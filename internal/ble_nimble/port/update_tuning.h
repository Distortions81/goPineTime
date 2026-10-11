#ifndef GOPINE_UPDATE_TUNING_H
#define GOPINE_UPDATE_TUNING_H

#include "update_links.h"

// Only the explicit updater needs a short interval. This runs after the host
// pump unwinds, once per owner connection; rejection leaves OTA usable with
// the central's original parameters and never changes a companion's link.
static inline void update_tune(struct update_link *link) {
    if(link->tuning_attempted)return;
    link->tuning_attempted=true;
    struct ble_gap_conn_desc desc;
    if(ble_gap_conn_find(link->handle,&desc))return;
    if(desc.conn_itvl<=12 && !desc.conn_latency)return;
    const struct ble_gap_upd_params params={
        .itvl_min=6,.itvl_max=12,.latency=0,.supervision_timeout=400
    };
    (void)ble_gap_update_params(link->handle,&params);
}

#endif
