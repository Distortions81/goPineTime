// A bounded, non-moving arena for NimBLE host allocations. This isolates C
// ownership from TinyGo's collector. Radio ISRs use NimBLE's fixed mbuf pools.
#include <stddef.h>
#include <stdint.h>
#include <string.h>

typedef struct __attribute__((aligned(8))) block { size_t size; struct block *next; int used; } block;
// Pinned GATT registration is the only dynamic NimBLE allocation. The real
// host harness peaks at 3056 bytes even with 64-bit pointers, three ACL slots
// and both companions active; retain at least 2KiB of tested arena headroom.
#define GOPINE_BLE_ARENA_SIZE 5120
static union { uint64_t align; unsigned char bytes[GOPINE_BLE_ARENA_SIZE]; } arena;
static block *head;
#ifdef GOPINE_BLE_ALLOC_AUDIT
static size_t allocation_peak;
size_t gopine_ble_alloc_peak(void) {return allocation_peak;}
size_t gopine_ble_alloc_capacity(void) {return sizeof(arena.bytes);}
#endif
void *gopine_ble_malloc(size_t n) {
    if (!n || n > sizeof(arena.bytes) - sizeof(block)) return NULL;
    n = (n + 7) & ~(size_t)7;
    if (!head) { head = (block *)arena.bytes; head->size = sizeof(arena.bytes) - sizeof(block); }
    for (block *b = head; b; b = b->next) {
        if (b->used || b->size < n) continue;
        if (b->size >= n + sizeof(block) + 8) {
            block *tail = (block *)((unsigned char *)(b + 1) + n);
            *tail = (block){b->size - n - sizeof(block), b->next, 0};
            b->next = tail; b->size = n;
        }
        b->used = 1;
#ifdef GOPINE_BLE_ALLOC_AUDIT
        size_t end=(unsigned char*)(b+1)-arena.bytes+b->size;
        if(end>allocation_peak)allocation_peak=end;
#endif
        return b + 1;
    }
    return NULL;
}
void gopine_ble_free(void *p) {
    if (!p) return;
    ((block *)p - 1)->used = 0;
    for (block *b = head; b && b->next;) {
        if (!b->used && !b->next->used) {
            b->size += sizeof(block) + b->next->size; b->next = b->next->next;
        } else b = b->next;
    }
}
void *gopine_ble_calloc(size_t n, size_t size) {
    if (size && n > SIZE_MAX / size) return NULL;
    void *p = gopine_ble_malloc(n * size);
    if (p) memset(p, 0, n * size);
    return p;
}
void *gopine_ble_realloc(void *p, size_t size) {
    if (!p) return gopine_ble_malloc(size);
    if (!size) { gopine_ble_free(p); return NULL; }
    size_t old = ((block *)p - 1)->size;
    if (old >= size) return p;
    void *next = gopine_ble_malloc(size);
    if (!next) return NULL;
    memcpy(next, p, old); gopine_ble_free(p); return next;
}

// POSIX 48-bit LCG used only for NimBLE scheduling jitter. This is not the
// source of cryptographic random bytes (NimBLE uses the nRF RNG for those).
long jrand48(unsigned short state[3]) {
    uint64_t x = (uint64_t)state[0] | (uint64_t)state[1] << 16 | (uint64_t)state[2] << 32;
    x = (x * UINT64_C(0x5deece66d) + 11) & UINT64_C(0xffffffffffff);
    state[0] = x; state[1] = x >> 16; state[2] = x >> 32;
    return (int32_t)(x >> 16);
}
