#!/usr/bin/env python3
"""Deterministic actual-sender benchmark; simulated times are not radio measurements."""
import asyncio
from contextlib import redirect_stdout
import heapq
import io
import json
from pathlib import Path
import struct
from types import SimpleNamespace

import direct_upload as direct


class VirtualLoop(asyncio.SelectorEventLoop):
    """Advance directly to scheduled transport/flash events, without sleeping."""
    def __init__(self):
        self.now = 0.0
        super().__init__()
        self.callback_error = None
        self.set_exception_handler(lambda loop, context: setattr(self, 'callback_error', context['exception']))

    def time(self):
        return self.now

    def _run_once(self):
        while self._scheduled and self._scheduled[0]._cancelled:
            heapq.heappop(self._scheduled)
        if not self._ready:
            if not self._scheduled:
                raise AssertionError('Virtual transport deadlocked without a scheduled event')
            self.now = max(self.now, self._scheduled[0]._when)
        super()._run_once()
        if self.callback_error:
            raise self.callback_error


class SimulatedWatch:
    """Separate native acceptance, ordered radio delivery, flash readback and ACK."""
    def __init__(self, image, *, capabilities=True, wnr=True, wnr_limit=244, fault=None, **timing):
        self.image = image
        self.flash = bytearray(len(image))
        self.timing = {key: value / 1000 for key, value in timing.items()}
        self.capabilities, self.wnr, self.fault = capabilities, wnr, fault
        self.cap_bytes = bytes((1, 3, 1, 0)) if fault == 'bad_caps' else bytes((1, 2, 1, 0))
        self.state, self.total, self.offset, self.session = direct.WAITING, 0, 0, 0
        self.accepted = 0
        self.queue = []
        self.generation = 0
        self.connections = self.native = self.max_native = self.max_queue = 0
        self.data_writes = self.response_writes = self.command_writes = self.dropped = 0
        self.first_data = self.last_commit = None
        self.data_offsets = []
        self.callback = None
        self.fault_used = False
        self.wire_at = self.flash_at = 0
        self.properties = {
            direct.CONTROL: SimpleNamespace(properties=['write', 'read'] if capabilities else ['write']),
            direct.DATA: SimpleNamespace(properties=['write', 'write-without-response'] if wnr else ['write'],
                                         max_write_without_response_size=wnr_limit),
        }
        self.services = SimpleNamespace(get_characteristic=lambda uuid: self.properties[uuid])

    @property
    def loop(self):
        return asyncio.get_running_loop()

    async def native_call(self, action, delay=0):
        self.native += 1
        self.max_native = max(self.max_native, self.native)
        assert self.native == 1, 'Sender overlapped native GATT operations'
        try:
            result = action()
            if delay:
                await asyncio.sleep(delay)
            return result
        finally:
            self.native -= 1

    def status(self):
        return struct.pack('<BBBBIII', 2, self.state, int(self.state == direct.FAILED), 244,
                           self.total, self.offset, self.session)

    def notify(self):
        raw, epoch = self.status(), self.generation
        if self.fault == 'reset' and self.offset >= 472:
            return
        if self.fault == 'bad_boundary' and self.offset and not self.fault_used:
            raw = bytearray(raw)
            struct.pack_into('<I', raw, 8, self.offset + 1)
            self.fault_used = True
        if self.fault == 'lost_notification' and self.offset == 236 and not self.fault_used:
            self.fault_used = True
            return
        def deliver():
            if epoch == self.generation and self.callback:
                self.callback(direct.STATUS, raw)
        self.loop.call_later(self.timing['one_way_ms'], deliver)

    async def connect(self):
        self.connections += 1
        self.accepted = self.offset
        self.wire_at = self.flash_at = self.loop.time()
        await asyncio.sleep(2 * self.timing['one_way_ms'])

    def disconnect(self):
        self.generation += 1
        self.queue.clear()
        self.callback = None
        self.accepted = self.offset
        if self.fault == 'reset':
            self.offset = self.total = self.session = 0
            self.state = direct.WAITING

    async def start_notify(self, char, callback):
        assert char == direct.STATUS
        await self.native_call(lambda: setattr(self, 'callback', callback), 2 * self.timing['one_way_ms'])

    async def stop_notify(self, char):
        await self.native_call(lambda: setattr(self, 'callback', None))

    async def read_gatt_char(self, char):
        return await self.native_call(lambda: self.cap_bytes if char == direct.CONTROL else self.status(),
                                      2 * self.timing['one_way_ms'])

    def receive(self, char, data, epoch):
        if epoch != self.generation:
            return False
        if char == direct.CONTROL:
            assert data[0] in (1, 2), 'No remote install or cancel'
            if data[0] == 1:
                _, self.session, self.total = struct.unpack('<BII', data)
                self.offset = self.accepted = 0
                self.state = direct.RECEIVING
                self.notify()
            else:
                assert not self.queue and self.offset == self.total
                assert self.flash == self.image, 'VERIFY before all bytes passed readback'
                self.state = direct.VERIFYING
                def verified():
                    if epoch == self.generation:
                        self.state = direct.READY
                        self.notify()
                self.loop.call_later(self.timing['verify_ms'], verified)
            return True
        session, offset = struct.unpack_from('<II', data)
        assert session == self.session and 8 < len(data) <= 244
        if self.fault == 'lost_packet' and not self.fault_used:
            self.fault_used = True
            self.dropped += 1
            return False
        capacity = 1 if self.fault == 'queue_pressure' and self.connections == 1 else 2
        if offset != self.accepted or len(self.queue) >= capacity:
            self.dropped += 1
            return False
        payload = bytes(data[8:])
        entry = (offset, payload)
        self.queue.append(entry)
        self.max_queue = max(self.max_queue, len(self.queue))
        self.accepted += len(payload)
        self.flash_at = max(self.flash_at, self.loop.time()) + self.timing['flash_ms']
        def commit():
            if epoch != self.generation or self.state != direct.RECEIVING:
                return
            assert self.queue.pop(0) == entry and offset == self.offset
            self.flash[offset:offset + len(payload)] = payload
            if self.fault == 'readback':
                self.state = direct.FAILED
            else:
                self.offset += len(payload)
            self.last_commit = self.loop.time()
            if self.fault == 'wrong_session' and not self.fault_used:
                self.session += 1
                self.fault_used = True
            self.notify()
        self.loop.call_at(self.flash_at, commit)
        return True

    async def write_gatt_char(self, char, data, response):
        self.native += 1
        self.max_native = max(self.max_native, self.native)
        assert self.native == 1
        try:
            if char == direct.DATA:
                self.data_writes += 1
                self.data_offsets.append(struct.unpack_from('<I', data, 4)[0])
                self.response_writes += response
                self.command_writes += not response
                if self.first_data is None:
                    self.first_data = self.loop.time()
            else:
                assert response
            epoch = self.generation
            arrival = max(self.loop.time() + self.timing['one_way_ms'], self.wire_at) + self.timing['wire_ms']
            self.wire_at = arrival
            done = self.loop.create_future()
            def deliver():
                accepted = self.receive(char, data, epoch)
                if response:
                    def reply():
                        if done.done():
                            return
                        if not accepted:
                            done.set_exception(ConnectionError('ATT queue pressure'))
                        else:
                            done.set_result(None)
                    self.loop.call_later(self.timing['one_way_ms'], reply)
            self.loop.call_at(arrival, deliver)
            if response:
                await done
            else:
                assert self.wnr and len(data) <= self.properties[direct.DATA].max_write_without_response_size
                await asyncio.sleep(self.timing['enqueue_ms'])
        finally:
            self.native -= 1


async def transfer(watch, max_connections=5):
    observed = 0
    progress = []
    timings = direct.TransferTiming()
    def acknowledge(done, total):
        nonlocal observed
        assert observed <= done <= watch.offset, 'Queued bytes were presented as durable progress'
        observed = done
        progress.append(done)
    for _ in range(max_connections):
        await watch.connect()
        try:
            await direct.send_connected(watch, watch.image, 42, acknowledge, observed, timings)
            return {'seconds': watch.loop.time(), 'data_seconds': timings.seconds['data'],
                    'verify_seconds': timings.seconds['verify'], 'connections': watch.connections,
                    'packets': watch.data_writes, 'wnr_packets': watch.command_writes,
                    'max_queue': watch.max_queue, 'max_native': watch.max_native, 'progress': progress,
                    'offsets': watch.data_offsets}
        except (TimeoutError, ConnectionError):
            watch.disconnect()
    raise AssertionError('Sender did not recover within bounded reconnect attempts')


def run_scenario(**options):
    fixture = json.loads((Path(__file__).resolve().parent.parent / 'testdata/ota_pipeline_scenarios.json').read_text())
    profile = options.pop('profile', 'latency_bound')
    size = options.pop('size', fixture['image_bytes'])
    image = bytes(index % 251 for index in range(size))
    loop = VirtualLoop()
    try:
        asyncio.set_event_loop(loop)
        watch = SimulatedWatch(image, **fixture[profile], **options)
        with redirect_stdout(io.StringIO()):
            result = loop.run_until_complete(transfer(watch))
        assert watch.flash == image and watch.offset == len(image)
        return result
    finally:
        loop.close()
        asyncio.set_event_loop(None)


def benchmark():
    result = {'description': 'Deterministic simulation; not measured watch throughput', 'profiles': {}}
    for profile in ('latency_bound', 'flash_bound'):
        modes = {name: run_scenario(profile=profile, **args) for name, args in (
            ('legacy', {'capabilities': False}), ('window_response', {'wnr': False}), ('window_wnr', {}))}
        for value in modes.values():
            value.pop('progress'); value.pop('offsets')
        modes['speedup'] = modes['legacy']['data_seconds'] / modes['window_wnr']['data_seconds']
        result['profiles'][profile] = modes
    return result


if __name__ == '__main__':
    print(json.dumps(benchmark(), indent=2))
