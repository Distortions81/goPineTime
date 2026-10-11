#include <assert.h>
#include <stdint.h>
#include <string.h>
#include <stddef.h>

void *gopine_ble_malloc(size_t);
void *gopine_ble_calloc(size_t, size_t);
void *gopine_ble_realloc(void *, size_t);
void gopine_ble_free(void *);

int main(void) {
    assert(!gopine_ble_malloc(SIZE_MAX));
    assert(!gopine_ble_calloc(SIZE_MAX, 2));
    for (int round = 0; round < 100; ++round) {
        unsigned char *a = gopine_ble_calloc(20, 1);
        assert(a && !((uintptr_t)a & 7));
        for (int i = 0; i < 20; i++) assert(a[i] == 0);
        memset(a, 0x5a, 20);
        a = gopine_ble_realloc(a, 400);
        assert(a);
        for (int i = 0; i < 20; i++) assert(a[i] == 0x5a);
        assert(!gopine_ble_realloc(a, 5120));
        assert(a[0] == 0x5a); // Failed realloc preserves the allocation.
        void *blocks[100]; int n = 0;
        while (n < 100 && (blocks[n] = gopine_ble_malloc(100))) n++;
        assert(n > 20 && n < 100);
        for (int i = 0; i < n; i += 2) gopine_ble_free(blocks[i]);
        for (int i = 1; i < n; i += 2) gopine_ble_free(blocks[i]);
        gopine_ble_free(a);
        void *large = gopine_ble_malloc(5000);
        assert(large); gopine_ble_free(large);
    }
    gopine_ble_free(NULL);
}
