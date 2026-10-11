// Deterministic actual-sender transport model; these are not radio measurements.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {setImmediate as turn} from 'node:timers/promises';
import {DirectUpdater, PHASE, UUIDS} from '../web/protocol.mjs';

const fixture = JSON.parse(readFileSync(new URL('../testdata/ota_pipeline_scenarios.json', import.meta.url)));
class VirtualClock {
  now = 0;
  next = 1;
  timers = new Map();
  setTimeout = (fn, delay = 0) => { const id = this.next++; this.timers.set(id, {at: this.now + delay, fn}); return id; };
  clearTimeout = id => this.timers.delete(id);
  sleep(ms) { return new Promise(resolve => this.setTimeout(resolve, ms)); }
  async drive(promise) {
    let finished = false, result, failure;
    promise.then(value => { result = value; finished = true; }, error => { failure = error; finished = true; });
    for (let events = 0; events < 100000; events++) {
      // Drain native promise microtasks; setImmediate adds no modeled delay.
      await turn();
      if (finished) { if (failure) throw failure; return result; }
      const first = [...this.timers.entries()].sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
      assert.ok(first, 'Virtual transport deadlocked without a scheduled event');
      this.timers.delete(first[0]); this.now = Math.max(this.now, first[1].at); first[1].fn();
    }
    throw new Error('Virtual transport exceeded its bounded event budget');
  }
}
class SimulatedWatch {
  constructor(clock, image, {capabilities = true, wnr = true, api = true, fault, ...timing}) {
    Object.assign(this, {clock, image, fault, timing, wnr});
    this.flash = new Uint8Array(image.length);
    this.phase = PHASE.WAITING; this.total = this.offset = this.session = this.accepted = 0;
    this.queue = []; this.epoch = 0; this.faultUsed = false;
    this.connections = this.native = this.maxNative = this.maxQueue = this.wnrPackets = 0;
    this.offsets = []; this.wireAt = this.flashAt = 0;
    this.statusNative = new EventTarget();
    this.statusNative.startNotifications = () => this.call(() => this.statusNative, 2 * timing.one_way_ms);
    this.statusNative.readValue = () => this.call(() => this.status(), 2 * timing.one_way_ms);
    this.control = {
      properties: {read: capabilities},
      readValue: () => this.call(() => new DataView(Uint8Array.from(fault === 'bad_caps' ? [1, 3, 1, 0] : [1, 2, 1, 0]).buffer), 2 * timing.one_way_ms),
      writeValueWithResponse: data => this.write(UUIDS.control, data, true),
    };
    this.data = {properties: {writeWithoutResponse: wnr}, writeValueWithResponse: data => this.write(UUIDS.data, data, true)};
    if (api) this.data.writeValueWithoutResponse = data => this.write(UUIDS.data, data, false);
    this.service = {getCharacteristic: uuid => this.call(() => {
      assert.ok([UUIDS.control, UUIDS.data, UUIDS.status].includes(uuid));
      return uuid === UUIDS.control ? this.control : uuid === UUIDS.data ? this.data : this.statusNative;
    })};
    this.server = {getPrimaryService: uuid => this.call(() => { assert.equal(uuid, UUIDS.service); return this.service; })};
    this.gatt = {connected: false, connect: () => this.call(() => {
      this.connections++; this.gatt.connected = true; this.accepted = this.offset;
      this.wireAt = this.flashAt = clock.now; return this.server;
    }, 2 * timing.one_way_ms), disconnect: () => this.disconnect()};
  }
  async call(action, delay = 0) {
    this.native++; this.maxNative = Math.max(this.native, this.maxNative);
    assert.equal(this.native, 1, 'Sender overlapped native GATT operations');
    try { const result = action(); if (delay) await this.clock.sleep(delay); return result; }
    finally { this.native--; }
  }
  status() {
    const bytes = new Uint8Array(16), view = new DataView(bytes.buffer);
    bytes.set([2, this.phase, Number(this.phase === PHASE.FAILED), 244]);
    view.setUint32(4, this.total, true); view.setUint32(8, this.offset, true); view.setUint32(12, this.session, true);
    return view;
  }
  notify() {
    const value = this.status(), epoch = this.epoch;
    if (this.fault === 'reset' && this.offset >= 472) return;
    if (this.fault === 'lost_notification' && this.offset === 236 && !this.faultUsed) { this.faultUsed = true; return; }
    if (this.fault === 'bad_boundary' && this.offset && !this.faultUsed) {
      value.setUint32(8, this.offset + 1, true); this.faultUsed = true;
    }
    this.clock.setTimeout(() => {
      if (epoch !== this.epoch || !this.gatt.connected) return;
      this.statusNative.value = value; this.statusNative.dispatchEvent(new Event('characteristicvaluechanged'));
    }, this.timing.one_way_ms);
  }
  disconnect() {
    this.gatt.connected = false; this.epoch++; this.queue = []; this.accepted = this.offset;
    if (this.fault === 'reset') { this.offset = this.total = this.session = 0; this.phase = PHASE.WAITING; }
  }
  receive(char, data, epoch) {
    if (epoch !== this.epoch) return false;
    const view = new DataView(data.buffer, data.byteOffset, data.byteLength);
    if (char === UUIDS.control) {
      assert.ok(data[0] === 1 || data[0] === 2, 'No remote install or cancel');
      if (data[0] === 1) {
        this.session = view.getUint32(1, true); this.total = view.getUint32(5, true);
        this.offset = this.accepted = 0; this.phase = PHASE.RECEIVING; this.notify();
      } else {
        assert.equal(this.queue.length, 0); assert.equal(this.offset, this.total);
        assert.deepEqual(this.flash, this.image, 'VERIFY before all bytes passed readback');
        this.phase = PHASE.VERIFYING;
        this.clock.setTimeout(() => { if (epoch === this.epoch) { this.phase = PHASE.READY; this.notify(); } }, this.timing.verify_ms);
      }
      return true;
    }
    const offset = view.getUint32(4, true);
    assert.equal(view.getUint32(0, true), this.session); assert.ok(data.length > 8 && data.length <= 244);
    if (this.fault === 'lost_packet' && !this.faultUsed) { this.faultUsed = true; return false; }
    const capacity = this.fault === 'queue_pressure' && this.connections === 1 ? 1 : 2;
    if (offset !== this.accepted || this.queue.length >= capacity) return false;
    const payload = data.slice(8), entry = {offset, payload};
    this.queue.push(entry); this.maxQueue = Math.max(this.maxQueue, this.queue.length); this.accepted += payload.length;
    this.flashAt = Math.max(this.flashAt, this.clock.now) + this.timing.flash_ms;
    this.clock.setTimeout(() => {
      if (epoch !== this.epoch || this.phase !== PHASE.RECEIVING) return;
      assert.equal(this.queue.shift(), entry); assert.equal(offset, this.offset);
      this.flash.set(payload, offset);
      if (this.fault === 'readback') this.phase = PHASE.FAILED;
      else this.offset += payload.length;
      if (this.fault === 'wrong_session' && !this.faultUsed) { this.session++; this.faultUsed = true; }
      this.notify();
    }, this.flashAt - this.clock.now);
    return true;
  }
  async write(char, data, response) {
    this.native++; this.maxNative = Math.max(this.native, this.maxNative); assert.equal(this.native, 1);
    try {
      if (char === UUIDS.data) {
        this.offsets.push(new DataView(data.buffer, data.byteOffset, data.byteLength).getUint32(4, true));
        this.wnrPackets += !response;
      } else assert.ok(response);
      const epoch = this.epoch;
      const at = Math.max(this.clock.now + this.timing.one_way_ms, this.wireAt) + this.timing.wire_ms;
      this.wireAt = at;
      let complete, reject;
      const done = response ? new Promise((resolve, fail) => { complete = resolve; reject = fail; }) : null;
      this.clock.setTimeout(() => {
        const accepted = this.receive(char, data, epoch);
        if (response) this.clock.setTimeout(() => accepted ? complete() : reject(new DOMException('ATT queue pressure', 'NetworkError')), this.timing.one_way_ms);
      }, at - this.clock.now);
      if (response) await done;
      else { assert.ok(this.wnr); await this.clock.sleep(this.timing.enqueue_ms); }
    } finally { this.native--; }
  }
}
export async function simulate({profile = 'latency_bound', size = fixture.image_bytes, ...options} = {}) {
  const clock = new VirtualClock(), image = Uint8Array.from({length: size}, (_, i) => i % 251);
  const original = {setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout, performance: globalThis.performance};
  globalThis.setTimeout = clock.setTimeout; globalThis.clearTimeout = clock.clearTimeout; globalThis.performance = {now: () => clock.now};
  const watch = new SimulatedWatch(clock, image, {...fixture[profile], ...options});
  const progress = [];
  const updater = new DirectUpdater({device: watch, image, session: 42, timing: {retryDelayMs: 0}, onProgress: done => {
    assert.ok(done <= watch.offset && done >= (progress.at(-1) ?? 0), 'Queued bytes were shown as durable progress'); progress.push(done);
  }});
  try {
    const result = await clock.drive(updater.run());
    assert.deepEqual(watch.flash, image); assert.equal(watch.offset, image.length);
    return {...result.timings, connections: watch.connections, packets: watch.offsets.length, offsets: watch.offsets,
      wnrPackets: watch.wnrPackets, maxQueue: watch.maxQueue, maxNative: watch.maxNative, progress};
  } finally { Object.assign(globalThis, original); }
}
export async function benchmark() {
  const result = {description: 'Deterministic simulation; not measured watch throughput', profiles: {}};
  for (const profile of ['latency_bound', 'flash_bound']) {
    const modes = {};
    for (const [mode, options] of [['legacy', {capabilities: false}], ['window_response', {wnr: false}], ['window_wnr', {}]]) {
      modes[mode] = await simulate({profile, ...options}); delete modes[mode].offsets; delete modes[mode].progress;
    }
    modes.speedup = modes.legacy.data / modes.window_wnr.data; result.profiles[profile] = modes;
  }
  return result;
}
