import test from 'node:test';
import assert from 'node:assert/strict';
import {DirectUpdater, UpdateError, decodeStatus, PHASE, UUIDS} from './protocol.mjs';

const timing = {retryDelayMs: 0, ackTimeoutMs: 30,
  verifyTimeoutMs: 30, noProgressTimeoutMs: 50, operationTimeoutMs: 100};
const image = Uint8Array.from({length: 101}, (_, i) => i);
function statusBytes({phase = 1, total = 0, offset = 0, session = 0, mtu = 20, error = 0, protocol = 2} = {}) {
  const bytes = new Uint8Array(16), view = new DataView(bytes.buffer);
  bytes.set([protocol, phase, error, mtu]);
  view.setUint32(4, total, true); view.setUint32(8, offset, true); view.setUint32(12, session, true);
  return new DataView(bytes.buffer);
}
class MockWatch {
  constructor(options = {}) {
    this.options = options;
    this.status = {phase: PHASE.WAITING, total: 0, offset: 0, session: 0, mtu: options.mtu ?? 20, ...options.status};
    this.writes = []; this.commands = []; this.received = new Uint8Array(options.imageLength ?? image.length);
    this.connections = 0; this.reads = 0; this.inflight = 0; this.maxInflight = 0;
    this.pending = null;
    this.gatt = {
      connected: false,
      connect: () => this.call(async () => {
        this.connections++;
        if (options.failConnect?.(this)) throw new DOMException('Disconnected', 'NetworkError');
        await options.onConnect?.(this);
        this.gatt.connected = true;
        return this.server;
      }),
      disconnect: () => { this.gatt.connected = false; options.onDisconnect?.(this); },
    };
    this.server = {getPrimaryService: uuid => this.call(async () => {
      assert.equal(uuid, UUIDS.service); await options.onService?.(this); return this.service;
    })};
    this.statusCharacteristic = new EventTarget();
    this.statusCharacteristic.readValue = () => this.read();
    this.statusCharacteristic.startNotifications = () => this.call(async () => this.statusCharacteristic);
    this.service = {getCharacteristic: uuid => this.call(() => {
      if (uuid === UUIDS.status) return this.statusCharacteristic;
      if (uuid === UUIDS.control) return {writeValueWithResponse: bytes => this.control(bytes)};
      if (uuid === UUIDS.data) return {writeValueWithResponse: bytes => this.write(bytes)};
      throw new Error('Unexpected characteristic');
    })};
  }
  async call(fn) {
    this.inflight++; this.maxInflight = Math.max(this.inflight, this.maxInflight);
    try { await Promise.resolve(); return await fn(); }
    finally { this.inflight--; }
  }
  read() {
    return this.call(async () => {
      this.reads++;
      if (this.pending) {
        if (this.pending.waits-- <= 0) { this.pending.commit(); this.pending = null; }
      }
      const result = await this.options.onRead?.(this);
      return result ?? statusBytes(this.status);
    });
  }
  notifyPending() {
    const pending = this.pending;
    if (!pending || !Number.isFinite(pending.waits)) return;
    setTimeout(async () => {
      if (this.pending !== pending || !this.gatt.connected) return;
      pending.commit(); this.pending = null;
      const result = await this.options.onRead?.(this);
      this.statusCharacteristic.value = result ?? statusBytes(this.status);
      this.statusCharacteristic.dispatchEvent(new Event('characteristicvaluechanged'));
    }, pending.waits);
  }
  control(bytes) {
    return this.call(async () => {
      const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
      const command = bytes[0], session = view.getUint32(1, true);
      this.commands.push(command);
      assert.ok(command === 1 || command === 2, 'Never send install, reset, or cancel commands');
      if (command === 1) {
        assert.equal(bytes.length, 9); assert.equal(this.status.phase, PHASE.WAITING);
        const total = view.getUint32(5, true);
        this.pending = {waits: this.options.ackDelay ?? 0, commit: () => {
          Object.assign(this.status, {phase: PHASE.RECEIVING, session, total});
        }};
      } else {
        assert.equal(bytes.length, 5); assert.equal(session, this.status.session);
        assert.equal(this.status.offset, this.status.total);
        this.status.phase = PHASE.VERIFYING;
        this.pending = {waits: this.options.verifyDelay ?? 0, commit: () => { this.status.phase = PHASE.READY; }};
      }
      await this.options.onControl?.(this, command);
      this.notifyPending();
    });
  }
  write(bytes) {
    return this.call(async () => {
      const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
      assert.equal(view.getUint32(0, true), this.status.session);
      const offset = view.getUint32(4, true);
      assert.equal(offset, this.status.offset);
      assert.equal(this.status.phase, PHASE.RECEIVING);
      assert.ok(bytes.length <= this.status.mtu);
      assert.ok(!this.pending);
      const payload = bytes.slice(8);
      this.writes.push({offset, payload});
      this.pending = {waits: this.options.ackDelay ?? 0, commit: () => {
        this.received.set(payload, offset); this.status.offset += payload.length;
      }};
      await this.options.onWrite?.(this, bytes);
      this.notifyPending();
    });
  }
}
function updater(watch, options = {}) {
  return new DirectUpdater({device: watch, image, session: 42, timing, ...options});
}

test('status parser respects DataView slices and rejects malformed fields', () => {
  const bytes = new Uint8Array(32); bytes.set(new Uint8Array(statusBytes({mtu: 200}).buffer), 7);
  assert.equal(decodeStatus(new DataView(bytes.buffer, 7, 16)).chunk, 192);
  assert.equal(decodeStatus(statusBytes({mtu: 20})).chunk, 12);
  assert.equal(decodeStatus(statusBytes({mtu: 8})).chunk, 0);
  for (const status of [new Uint8Array(15), statusBytes({protocol: 1}), statusBytes({phase: 6}),
    statusBytes({offset: 1}), statusBytes({error: 1}), statusBytes({phase: PHASE.FAILED})]) {
    assert.throws(() => decodeStatus(status), UpdateError);
  }
});
test('transfers sequentially with 12-byte payloads and delayed flash acknowledgements', async () => {
  const watch = new MockWatch({mtu: 20, ackDelay: 1, verifyDelay: 2}), progress = [], states = [];
  const instance = updater(watch, {onProgress: (done, total) => {
    assert.equal(done, watch.status.offset); assert.equal(total, image.length); progress.push(done);
  }, onState: ({phase}) => states.push(phase)});
  const result = await instance.run();
  assert.equal(result.session, 42); assert.equal(result.bytes, image.length);
  assert.ok(result.timings.total >= result.timings.data && result.timings.verify >= 0);
  assert.equal(watch.maxInflight, 1);
  assert.equal(watch.reads, watch.connections, "Only one status read per connection");
  assert.deepEqual(watch.writes.map(write => write.offset), [0, 12, 24, 36, 48, 60, 72, 84, 96]);
  assert.deepEqual(watch.received, image);
  assert.deepEqual(watch.commands, [1, 2]);
  assert.deepEqual(progress, [0, 12, 24, 36, 48, 60, 72, 84, 96, 101]);
  assert.deepEqual(states, ['connecting', 'connecting', 'connecting', 'sending', 'verifying', 'ready']);
  assert.equal(watch.gatt.connected, false);
});
test('reconnects after a lost write response using the authoritative received offset', async () => {
  let dropped = false;
  const watch = new MockWatch({onWrite: watch => {
    if (!dropped) {
      dropped = true; watch.pending.commit(); watch.pending = null;
      throw new DOMException('Response lost', 'NetworkError');
    }
  }});
  await updater(watch).run();
  assert.equal(watch.connections, 2);
  assert.deepEqual(watch.writes.map(write => write.offset), [0, 12, 24, 36, 48, 60, 72, 84, 96]);
  assert.deepEqual(watch.commands, [1, 2]);
  assert.deepEqual(watch.received, image);
  assert.equal(watch.maxInflight, 1);
});
test('resumes matching staged firmware and recognizes already verified images', async () => {
  for (const phase of [PHASE.RECEIVING, PHASE.VERIFYING, PHASE.READY]) {
    const watch = new MockWatch({status: {phase, total: image.length, offset: phase === PHASE.RECEIVING ? 84 : image.length, session: 42},
      onRead: watch => { if (watch.status.phase === PHASE.VERIFYING) watch.status.phase = PHASE.READY; }});
    await updater(watch).run();
    assert.equal(watch.commands.includes(1), false);
    assert.deepEqual(watch.writes.map(write => write.offset), phase === PHASE.RECEIVING ? [84, 96] : []);
  }
});
test('rejects another transfer before writing and rejects wrong acknowledgements', async () => {
  const wrongToken = new MockWatch({status: {phase: PHASE.RECEIVING, total: image.length, offset: 0, session: 99}});
  await assert.rejects(updater(wrongToken).run(), error => error instanceof UpdateError && !error.retryable && /different transfer/.test(error.message));
  assert.deepEqual(wrongToken.commands, []); assert.deepEqual(wrongToken.writes, []);
  const wrongAck = new MockWatch({onRead: watch => { if (watch.status.offset === 12) watch.status.offset++; }});
  await assert.rejects(updater(wrongAck).run(), /does not match this chunk/);
  assert.equal(wrongAck.writes.length, 1);
  const wrongIdentity = new MockWatch({onRead: watch => { if (watch.status.offset === 12) watch.status.session++; }});
  await assert.rejects(updater(wrongIdentity).run(), /different transfer/);
  assert.equal(wrongIdentity.writes.length, 1);
});
test('rejects incomplete verification and final identity changes', async () => {
  for (const phase of [PHASE.VERIFYING, PHASE.READY]) {
    const watch = new MockWatch({status: {phase, total: image.length, offset: 80, session: 42}});
    await assert.rejects(updater(watch).run(), /incomplete|complete image/);
    assert.equal(watch.writes.length, 0);
  }
  const watch = new MockWatch({onRead: watch => {
    if (watch.status.phase === PHASE.READY) watch.status.session = 99;
  }});
  await assert.rejects(updater(watch).run(), /different transfer/);
});
test('stop pauses after an acknowledged chunk and the same instance resumes', async () => {
  const watch = new MockWatch(); let paused = false;
  const instance = updater(watch, {onProgress: done => { if (done === 24 && !paused) { paused = true; instance.stop(); } }});
  await assert.rejects(instance.run(), {name: 'AbortError'});
  assert.equal(instance.acknowledged, 24); assert.equal(watch.writes.length, 2);
  assert.equal(instance.active, false); assert.equal(watch.gatt.connected, false);
  await instance.run();
  assert.deepEqual(watch.received, image);
  assert.deepEqual(watch.commands, [1, 2]);
});
test('stop during an unresolved write blocks overlap and cannot revive the old run', async () => {
  let releaseWrite, reachedWrite;
  const reached = new Promise(resolve => { reachedWrite = resolve; });
  const held = new Promise(resolve => { releaseWrite = resolve; });
  let first = true;
  const watch = new MockWatch({onWrite: async () => { if (first) { first = false; reachedWrite(); await held; } }});
  const instance = updater(watch);
  const run = instance.run();
  await reached;
  instance.stop();
  await assert.rejects(run, {name: 'AbortError'});
  await assert.rejects(instance.run(), error => error.retryable && /still closing/.test(error.message));
  assert.equal(watch.writes.length, 1);
  releaseWrite();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(watch.writes.length, 1);
  await instance.run();
  assert.equal(watch.maxInflight, 1);
  assert.deepEqual(watch.received, image);
});
test('does not silently restart when the watch clears previously acknowledged data', async () => {
  let stopped = false;
  const watch = new MockWatch();
  const instance = updater(watch, {onProgress: done => { if (done === 12 && !stopped) { stopped = true; instance.stop(); } }});
  await assert.rejects(instance.run(), {name: 'AbortError'});
  Object.assign(watch.status, {phase: PHASE.WAITING, total: 0, offset: 0, session: 0});
  await assert.rejects(instance.run(), /cleared this transfer/);
  assert.deepEqual(watch.commands, [1]);
});
test('bounds persistent missing acknowledgements without advancing to another chunk', async () => {
  const watch = new MockWatch({onWrite: watch => { watch.pending = null; }});
  await assert.rejects(updater(watch, {timing: {...timing, ackTimeoutMs: 5}}).run(), error => error.retryable && error.code === 'NO_PROGRESS');
  assert.ok(watch.connections > 1);
  assert.ok(watch.writes.length > 1);
  assert.ok(watch.writes.every(write => write.offset === 0));
  assert.equal(watch.maxInflight, 1);
  assert.deepEqual(watch.commands, [1]);
});
test('times out verification without claiming success or issuing install', async () => {
  const watch = new MockWatch({onControl: (watch, command) => { if (command === 2) watch.pending = null; }});
  const states = [];
  await assert.rejects(updater(watch, {timing: {...timing, verifyTimeoutMs: 5}, onState: state => states.push(state.phase)}).run(), error => error.code === 'NO_PROGRESS');
  assert.equal(states.includes('ready'), false);
  assert.deepEqual(watch.commands, [1, 2]);
});
test('bounds automatic reconnect attempts and makes their failure resumable', async () => {
  const watch = new MockWatch({failConnect: () => true});
  const states = [];
  await assert.rejects(updater(watch, {timing: {...timing, noProgressTimeoutMs: 5}, onState: state => states.push(state)}).run(),
    error => error.retryable && error.code === 'NO_PROGRESS' && error.stage === 'connecting to the watch' && /Disconnected/.test(error.message));
  assert.ok(states.some(state => state.phase === 'reconnecting' && /connecting to the watch.*Disconnected/.test(state.message)));
  assert.ok(watch.connections > 1); assert.equal(watch.writes.length, 0);
});
test('recovers an acknowledgement timeout without retransmitting committed bytes', async () => {
  let blocked = true;
  const watch = new MockWatch({onWrite: watch => {
    if (blocked) watch.pending.waits = Infinity;
  }, onDisconnect: watch => {
    if (blocked && watch.pending) {
      watch.pending.commit(); watch.pending = null; blocked = false;
    }
  }});
  const instance = updater(watch, {timing: {...timing, ackTimeoutMs: 5, noProgressTimeoutMs: 200}});
  await instance.run();
  assert.equal(watch.connections, 2);
  assert.equal(watch.maxInflight, 1);
  assert.deepEqual(watch.writes.map(write => write.offset), [0, 12, 24, 36, 48, 60, 72, 84, 96]);
  assert.deepEqual(watch.commands, [1, 2]);
  assert.deepEqual(watch.received, image);
});
test('recovers a verification timeout by reading READY without repeating VERIFY', async () => {
  const watch = new MockWatch({verifyDelay: Infinity, onDisconnect: watch => {
    if (watch.status.phase === PHASE.VERIFYING && watch.pending) {
      watch.pending.commit(); watch.pending = null;
    }
  }});
  const instance = updater(watch, {timing: {...timing, verifyTimeoutMs: 5, noProgressTimeoutMs: 200}});
  await instance.run();
  assert.equal(watch.connections, 2);
  assert.deepEqual(watch.commands, [1, 2]);
  assert.equal(watch.maxInflight, 1);
  assert.deepEqual(watch.received, image);
});
test('allows connection and service discovery longer than ordinary requests', async () => {
  const delay = () => new Promise(resolve => setTimeout(resolve, 15));
  const watch = new MockWatch({onConnect: delay, onService: delay});
  await updater(watch, {timing: {...timing, operationTimeoutMs: 5, connectTimeoutMs: 100, noProgressTimeoutMs: 200}}).run();
  assert.equal(watch.connections, 1);
  assert.deepEqual(watch.received, image);
});
test('drains timed-out writes that resolve or reject before reconnecting from committed bytes', async () => {
  for (const outcome of ['resolve', 'reject']) {
    let releaseWrite, first = true, releaseScheduled = false;
    const held = new Promise((resolve, reject) => {
      releaseWrite = outcome === 'resolve' ? resolve : () => reject(new DOMException('Late disconnect', 'NetworkError'));
    });
    const watch = new MockWatch({onWrite: async () => {
      if (first) { first = false; await held; }
    }, onDisconnect: watch => {
      if (!first && !releaseScheduled && watch.pending) {
        releaseScheduled = true;
        watch.pending.commit(); watch.pending = null;
        setTimeout(releaseWrite, 5);
      }
    }});
    const pending = [];
    const instance = updater(watch, {onPendingChange: count => pending.push(count),
      timing: {...timing, operationTimeoutMs: 5, cleanupTimeoutMs: 50, noProgressTimeoutMs: 200}});
    await instance.run();
    assert.equal(watch.connections, 2);
    assert.equal(watch.maxInflight, 1);
    assert.equal(instance.pendingOperations, 0);
    assert.ok(pending.every(count => count === 0 || count === 1));
    assert.equal(pending.at(-1), 0);
    assert.ok(pending.every((count, index) => count === (index % 2 === 0 ? 1 : 0)), 'Each started operation notifies a matching settlement');
    assert.deepEqual(watch.writes.map(write => write.offset), [0, 12, 24, 36, 48, 60, 72, 84, 96]);
    assert.deepEqual(watch.received, image);
  }
});
test('connection and service discovery failures retain different diagnostic stages', async () => {
  const watch = new MockWatch({onService: () => { throw new DOMException('Service discovery disconnected', 'NetworkError'); }});
  const states = [];
  await assert.rejects(updater(watch, {timing: {...timing, noProgressTimeoutMs: 20}, onState: state => states.push(state)}).run(),
    error => error.code === 'NO_PROGRESS' && error.stage === 'finding the update service' && /Service discovery disconnected/.test(error.message));
  assert.ok(states.some(state => state.phase === 'reconnecting' && /finding the update service.*Service discovery disconnected/.test(state.message)));
  assert.equal(watch.writes.length, 0);
  assert.equal(watch.maxInflight, 1);
});
test('pauses boundedly for a native operation that does not settle, with a useful stage', async () => {
  let releaseConnection;
  const held = new Promise(resolve => { releaseConnection = resolve; });
  const watch = new MockWatch({onConnect: () => held});
  const instance = updater(watch, {timing: {...timing, connectTimeoutMs: 5, cleanupTimeoutMs: 5, noProgressTimeoutMs: 200}});
  await assert.rejects(instance.run(), error => error.retryable && error.code === 'BLUETOOTH_CLOSING' && error.stage === 'connecting to the watch');
  assert.equal(instance.pendingOperations, 1);
  await assert.rejects(instance.run(), error => error.code === 'BLUETOOTH_CLOSING');
  assert.equal(watch.connections, 1);
  releaseConnection();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(instance.pendingOperations, 0);
  assert.equal(watch.gatt.connected, false, 'A late connection must not revive the retired run');
  await instance.run();
  assert.equal(watch.maxInflight, 1);
  assert.deepEqual(watch.received, image);
});
test('pause interrupts native-operation cleanup without reconnecting', async () => {
  let releaseWrite, reachedCleanup;
  const held = new Promise(resolve => { releaseWrite = resolve; });
  const cleanup = new Promise(resolve => { reachedCleanup = resolve; });
  const watch = new MockWatch({onWrite: () => held});
  const instance = updater(watch, {timing: {...timing, operationTimeoutMs: 5, cleanupTimeoutMs: 200, noProgressTimeoutMs: 200},
    onState: ({phase}) => { if (phase === 'reconnecting') reachedCleanup(); }});
  const run = instance.run();
  await cleanup;
  instance.stop();
  await assert.rejects(run, {name: 'AbortError'});
  assert.equal(watch.connections, 1);
  assert.equal(watch.writes.length, 1);
  assert.equal(instance.pendingOperations, 1);
  releaseWrite();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(instance.pendingOperations, 0);
  assert.equal(watch.maxInflight, 1);
});
test('caps even a successful slow connection by the overall no-progress budget', async () => {
  const watch = new MockWatch({onConnect: () => new Promise(resolve => setTimeout(resolve, 30))});
  const instance = updater(watch, {timing: {...timing, connectTimeoutMs: 200, cleanupTimeoutMs: 100, noProgressTimeoutMs: 5}});
  await assert.rejects(instance.run(), error => error.retryable && ['NO_PROGRESS', 'BLUETOOTH_CLOSING'].includes(error.code));
  assert.equal(watch.connections, 1);
  assert.equal(watch.writes.length, 0);
  await new Promise(resolve => setTimeout(resolve, 35));
  assert.equal(instance.pendingOperations, 0);
  assert.equal(watch.gatt.connected, false);
});
test('copies the firmware and rejects concurrent run calls', async () => {
  const source = image.slice(), watch = new MockWatch();
  const instance = updater(watch, {image: source});
  source.fill(255);
  const running = instance.run();
  await assert.rejects(instance.run(), /already running/);
  await running;
  assert.deepEqual(watch.received, image);
});

test('uses 236-byte payloads and only one status read at MTU 247', async () => {
  const largeImage = Uint8Array.from({length: 500}, (_, index) => index & 255);
  const watch = new MockWatch({mtu: 244, imageLength: largeImage.length});
  await updater(watch, {image: largeImage}).run();
  assert.deepEqual(watch.writes.map(write => write.payload.length), [236, 236, 28]);
  assert.deepEqual(watch.received, largeImage);
  assert.equal(watch.reads, 1);
  assert.equal(watch.maxInflight, 1);
});
test('rejects protocol-1 receivers before sending any firmware', async () => {
  const watch = new MockWatch({onRead: () => statusBytes({protocol: 1})});
  await assert.rejects(updater(watch).run(), /0.3.22.*protocol 2/);
  assert.deepEqual(watch.writes, []);
  assert.deepEqual(watch.commands, []);
});
