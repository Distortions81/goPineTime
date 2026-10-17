// Execute the pinned nRF52 PHY against mapped register memory. This checks
// actual register writes, not radio timing, DMA side effects or current draw.
#include <assert.h>
#include <stdio.h>
#include <sys/mman.h>
#include "nimble/nimble_npl.h"
void ble_npl_hw_set_isr(int irq, void (*fn)(void)) {(void)irq;(void)fn;}
#include "ble_phy.c"
#include "../port/power.h"
int ble_hw_rng_stop(void) {return 0;}
uint8_t g_nrf_num_irks;
uint32_t g_nrf_irk_list[16];
int main(void) {
    void *registers=mmap((void*)0x40000000,0x20000,PROT_READ|PROT_WRITE,
                         MAP_PRIVATE|MAP_ANONYMOUS|MAP_FIXED_NOREPLACE,-1,0);
    assert(registers==(void*)0x40000000);
    assert(&NRF_CCM->ENABLE==&NRF_AAR->ENABLE);
    assert(&NRF_CCM->TASKS_STOP==&NRF_AAR->TASKS_STOP);
    uint8_t key[16]={0},iv[8]={0};
    assert(!ble_phy_init());
    ble_phy_encrypt_enable(0,iv,key,0);
    assert(NRF_CCM->ENABLE==CCM_ENABLE_ENABLE_Enabled);
    // The old final shutdown sequence disables the PHY, but the controller
    // reset also calls PHY init. Neither operation clears CCM/AAR ENABLE.
    ble_phy_disable();
    assert(!ble_phy_init());
    ble_phy_disable();
    assert(NRF_CCM->ENABLE==CCM_ENABLE_ENABLE_Enabled);
    puts("reproduced: CCM remains enabled after PHY disable/init/disable");
    ble_phy_encrypt_disable();
    ble_phy_resolv_list_enable();
    ble_phy_rx_xcvr_setup();
    assert(NRF_AAR->ENABLE==AAR_ENABLE_ENABLE_Enabled);
    ble_phy_disable();
    assert(!ble_phy_init());
    ble_phy_disable();
    assert(NRF_AAR->ENABLE==AAR_ENABLE_ENABLE_Enabled);
    puts("reproduced: AAR remains enabled after PHY disable/init/disable");
    for(unsigned i=0;i<20;i++) {
        assert(!ble_phy_init());
        NRF_RADIO->TASKS_DISABLE=0;
        NRF_TIMER0->TASKS_STOP=0;
        NRF_TIMER0->TASKS_SHUTDOWN=0;
        NRF_CLOCK->TASKS_HFCLKSTOP=0;
        if(i%2) {
            ble_phy_encrypt_disable();
            ble_phy_resolv_list_enable();
            ble_phy_rx_xcvr_setup();
            assert(NRF_AAR->ENABLE==AAR_ENABLE_ENABLE_Enabled);
        } else {
            ble_phy_resolv_list_disable();
            ble_phy_encrypt_enable(0,iv,key,0);
            ble_phy_rx_xcvr_setup();
            assert(NRF_CCM->ENABLE==CCM_ENABLE_ENABLE_Enabled);
        }
        NRF_CCM->TASKS_STOP=0;
        gopine_ble_power_down();
        assert(NRF_CCM->ENABLE==CCM_ENABLE_ENABLE_Disabled);
        assert(NRF_AAR->ENABLE==AAR_ENABLE_ENABLE_Disabled);
        assert(NRF_CCM->TASKS_STOP==1 && !g_ble_phy_data.phy_encrypted);
        assert(NRF_RADIO->TASKS_DISABLE==1);
        assert(NRF_TIMER0->TASKS_STOP==1 && NRF_TIMER0->TASKS_SHUTDOWN==1);
        assert(NRF_CLOCK->TASKS_HFCLKSTOP==1);
        // The next session can configure encryption again.
        ble_phy_encrypt_enable(0,iv,key,0);
        assert(NRF_CCM->ENABLE==CCM_ENABLE_ENABLE_Enabled);
        gopine_ble_power_down();
    }
    puts("shutdown clears CCM/AAR, stops radio/timer/crystal, and permits restart");
    assert(!munmap(registers,0x20000));
    return 0;
}
