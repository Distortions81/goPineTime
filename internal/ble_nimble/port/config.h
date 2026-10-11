#define NRF52832_XXAA 1
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define malloc gopine_ble_malloc
#define calloc gopine_ble_calloc
#define realloc gopine_ble_realloc
#define free gopine_ble_free
#define NIMBLE_CFG_CONTROLLER 1
#define NIMBLE_CFG_HOST 1
#define MYNEWT_VAL_BLE_ROLE_CENTRAL 0
#define MYNEWT_VAL_BLE_ROLE_OBSERVER 0
// GAP peripheral and GATT client are independent roles. NimBLE defaults these
// client operations to BLE_ROLE_CENTRAL; ANCS consumes the phone's services on
// our existing peripheral connection, without scanning or initiating links.
#define MYNEWT_VAL_BLE_GATT_DISC_SVC_UUID 1
#define MYNEWT_VAL_BLE_GATT_DISC_ALL_CHRS 1
#define MYNEWT_VAL_BLE_GATT_DISC_ALL_DSCS 1
#define MYNEWT_VAL_BLE_GATT_WRITE 1
// Two companions use Phone mode; OTA reserves a third slot for its uploader.
#define MYNEWT_VAL_BLE_MAX_CONNECTIONS 3
#define MYNEWT_VAL_BLE_HS_AUTO_START 0
// Match InfiniTime: let the controller release HFXO between radio events.
#define MYNEWT_VAL_BLE_LL_RFMGMT_ENABLE_TIME 1500
#define MYNEWT_VAL_BLE_HS_STOP_ON_SHUTDOWN_TIMEOUT 500
#define MYNEWT_VAL_BLE_SM_LEGACY 0
#define MYNEWT_VAL_BLE_SM_SC 1
// Saved peers can restore encryption concurrently. sm.c separately limits
// fresh pairing to one PIN exchange, so restoration cannot starve another LTK.
#define MYNEWT_VAL_BLE_SM_MAX_PROCS 2
#define MYNEWT_VAL_BLE_SM_BONDING 1
#define MYNEWT_VAL_BLE_SM_IO_CAP BLE_HS_IO_DISPLAY_ONLY
#define MYNEWT_VAL_BLE_SM_MITM 1
#define MYNEWT_VAL_BLE_SM_OUR_KEY_DIST 3
#define MYNEWT_VAL_BLE_SM_THEIR_KEY_DIST 3
#define MYNEWT_VAL_BLE_LL_CFG_FEAT_LE_ENCRYPTION 1
#define MYNEWT_VAL_BLE_LL_CFG_FEAT_LL_PRIVACY 1
#define MYNEWT_VAL_BLE_LL_CFG_FEAT_DATA_LEN_EXT 0
#define MYNEWT_VAL_BLE_LL_CFG_FEAT_LE_2M_PHY 0
#define MYNEWT_VAL_BLE_LL_CFG_FEAT_LE_CODED_PHY 0
#define MYNEWT_VAL_BLE_ATT_PREFERRED_MTU 247
#define MYNEWT_VAL_BLE_L2CAP_COC_MAX_NUM 0
#define MYNEWT_VAL_MSYS_1_BLOCK_COUNT 8
#define MYNEWT_VAL_MSYS_1_BLOCK_SIZE 128
#define MYNEWT_VAL_BLE_ACL_BUF_SIZE 64
#define MYNEWT_VAL_BLE_ACL_BUF_COUNT 4

#define MYNEWT_VAL_BLE_STORE_MAX_BONDS 2
#define MYNEWT_VAL_BLE_STORE_MAX_CCCDS 8
