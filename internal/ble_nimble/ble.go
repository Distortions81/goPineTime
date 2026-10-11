//go:build pinetime && bletime && !provision

package blenimble

/*
#include <stdint.h>
int gopine_ble_start(const uint8_t *value, uint8_t battery, uint32_t window_ms);
int gopine_ble_start_weather(const uint8_t *value, uint8_t battery, uint32_t window_ms);
int gopine_ble_take_weather(uint8_t *value);
void gopine_ble_stop(void);
void gopine_ble_poll(void);
void gopine_ble_controller_poll(void);
int gopine_ble_take(uint8_t *value, uint32_t *age);
int gopine_ble_error(void);
int gopine_ble_busy(void);
int gopine_ble_start_phone(const uint8_t *, uint8_t, uint32_t);
void gopine_ble_phone_setup(void);
int gopine_ble_take_music(uint8_t *);
int gopine_ble_take_notification(uint8_t *);
int gopine_ble_take_apple_notification(uint8_t *);
void gopine_ble_update_battery(uint8_t);
void gopine_ble_set_firmware_version(const uint8_t *, unsigned);
int gopine_ble_start_update(uint8_t);
int gopine_ble_take_update(uint8_t *, unsigned *);
void gopine_ble_update_status(const uint8_t *);
int gopine_ble_music_command(uint8_t,uint32_t);
int gopine_ble_updates(void);
void gopine_ble_ack_updates(void);
uint32_t gopine_ble_idle_ms(void);
uint32_t gopine_ble_pairing_code(void);
uint32_t gopine_ble_pairing_diagnostics(void);
uint16_t gopine_ble_pairing_progress(void);
int gopine_ble_bond_status(void);
unsigned gopine_bond_count(void);
unsigned gopine_ble_peer_count(void);
unsigned gopine_ble_trusted_peer_count(void);
int gopine_ble_cancel_pairing(void);
int gopine_ble_forget_phone(void);
*/
import "C"

// SetFirmwareVersion copies the build's version into the public Device
// Information service before the radio starts. C retains no Go pointer.
func SetFirmwareVersion(value string) {
	var raw [48]byte
	n := copy(raw[:], value)
	C.gopine_ble_set_firmware_version((*C.uint8_t)(&raw[0]), C.uint(n))
}

func StartWeather(value [10]byte, battery uint8, windowMS uint32) int {
	return int(C.gopine_ble_start_weather((*C.uint8_t)(&value[0]), C.uint8_t(battery), C.uint32_t(windowMS)))
}
func TakeWeather() (value [53]byte, size int) {
	size = int(C.gopine_ble_take_weather((*C.uint8_t)(&value[0])))
	return
}

func Start(value [10]byte, battery uint8, windowMS uint32) int {
	return int(C.gopine_ble_start((*C.uint8_t)(&value[0]), C.uint8_t(battery), C.uint32_t(windowMS)))
}
func Stop()              { C.gopine_ble_stop() }
func Service()           { C.gopine_ble_poll() }
func ServiceController() { C.gopine_ble_controller_poll() }
func Busy() bool         { return C.gopine_ble_busy() != 0 }
func Error() int         { return int(C.gopine_ble_error()) }
func Take() (value [10]byte, size int, age uint32) {
	var elapsed C.uint32_t
	size = int(C.gopine_ble_take((*C.uint8_t)(&value[0]), &elapsed))
	return value, size, uint32(elapsed)
}

func PhoneSetup() { C.gopine_ble_phone_setup() }

func StartPhone(value [10]byte, battery uint8, windowMS uint32) int {
	return int(C.gopine_ble_start_phone((*C.uint8_t)(&value[0]), C.uint8_t(battery), C.uint32_t(windowMS)))
}
func TakeMusic() (value [87]byte, changed bool) {
	changed = C.gopine_ble_take_music((*C.uint8_t)(&value[0])) != 0
	return
}
func TakeNotification() (value [103]byte, size int) {
	size = int(C.gopine_ble_take_notification((*C.uint8_t)(&value[0])))
	return
}
func TakeAppleNotification() (value [148]byte, size int) {
	size = int(C.gopine_ble_take_apple_notification((*C.uint8_t)(&value[0])))
	return
}
func UpdateBattery(value uint8)     { C.gopine_ble_update_battery(C.uint8_t(value)) }
func StartUpdate(battery uint8) int { return int(C.gopine_ble_start_update(C.uint8_t(battery))) }
func TakeUpdate(data *[244]byte) (kind, size int) {
	var n C.uint
	kind = int(C.gopine_ble_take_update((*C.uint8_t)(&data[0]), &n))
	size = int(n)
	return
}
func UpdateStatus(status [16]byte) { C.gopine_ble_update_status((*C.uint8_t)(&status[0])) }
func MusicCommand(command byte, generation uint32) int {
	return int(C.gopine_ble_music_command(C.uint8_t(command), C.uint32_t(generation)))
}
func HasUpdate() bool            { return C.gopine_ble_updates() != 0 }
func AcknowledgeUpdates()        { C.gopine_ble_ack_updates() }
func IdleMS() uint32             { return uint32(C.gopine_ble_idle_ms()) }
func PairingCode() uint32        { return uint32(C.gopine_ble_pairing_code()) }
func PairingDiagnostics() uint32 { return uint32(C.gopine_ble_pairing_diagnostics()) }
func PairingProgress() uint16    { return uint16(C.gopine_ble_pairing_progress()) }
func BondStatus() int            { return int(C.gopine_ble_bond_status()) }
func BondCount() int             { return int(C.gopine_bond_count()) }
func PeerCount() int             { return int(C.gopine_ble_peer_count()) }
func TrustedPeerCount() int      { return int(C.gopine_ble_trusted_peer_count()) }
func CancelPairing() int         { return int(C.gopine_ble_cancel_pairing()) }
func ForgetPhone() int           { return int(C.gopine_ble_forget_phone()) }
