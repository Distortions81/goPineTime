// goPine direct update protocol v2. The watch alone can INSTALL and KEEP.
import {MAX_IMAGE_BYTES} from './firmware.mjs';

const base = '-78fc-48fe-8e23-433b3a1942d0';
export const UUIDS = Object.freeze({
  service: `00060000${base}`, control: `00060001${base}`,
  data: `00060002${base}`, status: `00060003${base}`,
});
export const PHASE = Object.freeze({WAITING: 1, RECEIVING: 2, VERIFYING: 3, READY: 4, FAILED: 5});

export class UpdateError extends Error {
  constructor(message, {retryable = false, code = 'UPDATE_REJECTED', stage} = {}) {
    super(message); this.name = 'UpdateError'; this.retryable = retryable;
    this.code = code; this.stage = stage;
  }
}
const check = (ok, message) => { if (!ok) throw new UpdateError(message); };
const abortError = () => new DOMException('Transfer paused. Keep this page open to resume.', 'AbortError');

/** Call directly from the user's click handler to preserve browser activation. */
export function requestWatch() {
  check(globalThis.isSecureContext !== false, 'Bluetooth updates require HTTPS.');
  check(globalThis.navigator?.bluetooth?.requestDevice, 'Use a browser with Web Bluetooth, such as Chrome or Edge.');
  return navigator.bluetooth.requestDevice({filters: [{services: [UUIDS.service]}]});
}

export function decodeStatus(value) {
  const bytes = value instanceof ArrayBuffer ? new Uint8Array(value) :
    ArrayBuffer.isView(value) ? new Uint8Array(value.buffer, value.byteOffset, value.byteLength) : null;
  check(bytes?.length === 16 && bytes[0] === 2 && bytes[1] >= 1 && bytes[1] <= 5, 'This updater requires goPine 0.3.22 or newer (OTA protocol 2).');
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const status = {phase: bytes[1], total: view.getUint32(4, true), offset: view.getUint32(8, true),
    session: view.getUint32(12, true), chunk: Math.max(0, Math.min(236, bytes[3] - 8))};
  check(status.offset <= status.total, 'Watch reported an invalid received-byte count.');
  check(status.phase !== PHASE.FAILED && bytes[2] === 0, 'Watch stopped the update. Read its message, then use Retry on the watch.');
  return status;
}

function randomSession() {
  const value = new Uint32Array(1);
  do { crypto.getRandomValues(value); } while (value[0] === 0);
  return value[0];
}
function packet(command, session, total) {
  const result = new Uint8Array(total === undefined ? 5 : 9);
  const view = new DataView(result.buffer);
  result[0] = command;
  view.setUint32(1, session, true);
  if (total !== undefined) view.setUint32(5, total, true);
  return result;
}

/**
 * Keep this instance to resume a paused/interrupted transfer: its private image
 * copy and random session token are fixed for its lifetime. run() reconnects to
 * the selected device only; stop() disconnects without discarding watch bytes.
 * onProgress reports only flash bytes acknowledged by the watch. No callback
 * claims successful installation: run() resolves after receiver verification.
 */
export class DirectUpdater {
  #image;
  #session;
  #active = false;
  #controller;
  #acknowledged = 0;
  #deadline = 0;
  #pendingOperations = 0;
  #stage = 'connecting to the watch';
  #lastInterruption;
  #timing;
  #started = 0;
  #measurePhase = null;
  #measureAt = 0;
  #seconds = {data: 0, verify: 0};
  constructor({device, image, onProgress = () => {}, onState = () => {}, onPendingChange = () => {}, session = randomSession(), timing = {}}) {
    check(device?.gatt, 'Select a Bluetooth watch first.');
    check(image instanceof Uint8Array && image.length >= 80 && image.length <= MAX_IMAGE_BYTES, 'Select and validate a firmware package first.');
    check(Number.isInteger(session) && session > 0 && session <= 0xffffffff, 'Invalid transfer session.');
    this.device = device;
    this.#image = image.slice();
    this.#session = session;
    this.onProgress = onProgress;
    this.onState = onState;
    this.onPendingChange = onPendingChange;
    this.#timing = {...{ackTimeoutMs: 15000, verifyTimeoutMs: 45000, noProgressTimeoutMs: 90000,
      retryDelayMs: 1000, operationTimeoutMs: 15000,
      connectTimeoutMs: 30000, cleanupTimeoutMs: 5000}, ...timing};
    check(Object.values(this.#timing).every(value => Number.isFinite(value) && value >= 0 && value <= 300000), 'Invalid update timeout.');
  }
  get session() { return this.#session; }
  get acknowledged() { return this.#acknowledged; }
  get active() { return this.#active; }
  get timings() { return {total: (performance.now() - this.#started) / 1000, ...this.#seconds}; }
  #measure(phase) {
    const now = performance.now();
    if (this.#measurePhase) this.#seconds[this.#measurePhase] += (now - this.#measureAt) / 1000;
    this.#measurePhase = phase; this.#measureAt = now;
  }
  get pendingOperations() { return this.#pendingOperations; }
  #state(phase, message) { this.onState({phase, message, ...(phase === 'ready' ? {timings: this.timings} : {})}); }
  #checkRunning() { if (this.#controller.signal.aborted) throw abortError(); }
  #disconnect() { try { this.device.gatt.disconnect(); } catch { /* Already disconnected. */ } }
  stop() { this.#controller?.abort(); this.#disconnect(); }
  disconnect() { this.stop(); }

  #noProgressError(stage) {
    const detail = this.#lastInterruption ? ` Last Bluetooth error: ${this.#lastInterruption}.` : '';
    return new UpdateError(`The watch made no progress while ${stage}. Keep its update screen open, then resume on this page.${detail}`,
      {retryable: true, code: 'NO_PROGRESS', stage});
  }
  #remaining(stage) {
    const remaining = this.#deadline - performance.now();
    if (remaining <= 0) throw this.#noProgressError(stage);
    return remaining;
  }
  #pendingChange(amount) {
    this.#pendingOperations += amount;
    this.onPendingChange(this.#pendingOperations);
  }
  // Every native GATT call settles before another can start. Disconnecting can
  // take time to reject a stalled browser promise, so retries drain it first.
  async #operation(start, stage, timeoutMs = this.#timing.operationTimeoutMs) {
    this.#checkRunning();
    this.#stage = stage;
    const timeoutAfter = Math.min(timeoutMs, this.#remaining(stage));
    const signal = this.#controller.signal;
    let timeout, onAbort, retired = false;
    this.#pendingChange(1);
    const operation = Promise.resolve().then(() => { if (signal.aborted) throw abortError(); return start(); });
    operation.then(() => {
      if (retired) this.#disconnect();
      this.#pendingChange(-1);
    }, () => { this.#pendingChange(-1); });
    try {
      return await Promise.race([operation, new Promise((_, reject) => {
        onAbort = () => { retired = true; reject(abortError()); };
        signal.addEventListener('abort', onAbort, {once: true});
        timeout = setTimeout(() => {
          retired = true;
          reject(new UpdateError(`Bluetooth timed out while ${stage}. Reconnecting to the watch…`,
            {retryable: true, code: 'BLUETOOTH_TIMEOUT', stage}));
        }, timeoutAfter);
      })]);
    } finally {
      clearTimeout(timeout);
      signal.removeEventListener('abort', onAbort);
    }
  }
  async #drainOperations(stage) {
    const deadline = Math.min(this.#deadline, performance.now() + this.#timing.cleanupTimeoutMs);
    while (this.#pendingOperations > 0) {
      this.#checkRunning();
      const remaining = deadline - performance.now();
      if (remaining <= 0) throw new UpdateError('The previous Bluetooth request is still closing. Keep this page open, then resume after it closes.',
        {retryable: true, code: 'BLUETOOTH_CLOSING', stage});
      await this.#sleep(Math.min(25, remaining));
    }
  }
  async #sleep(ms) {
    this.#checkRunning();
    const signal = this.#controller.signal;
    await new Promise((resolve, reject) => {
      const onAbort = () => { clearTimeout(timer); reject(abortError()); };
      const timer = setTimeout(() => { signal.removeEventListener('abort', onAbort); resolve(); }, ms);
      signal.addEventListener('abort', onAbort, {once: true});
    });
    this.#checkRunning();
  }
  #identity(status) {
    check(status.session === this.#session && status.total === this.#image.length,
      'A different transfer is staged. Use Cancel or Retry on the watch, then start a new transfer.');
    check(status.offset >= this.#acknowledged, 'The watch lost previously acknowledged bytes. Use Retry on the watch and start a new transfer.');
  }
  #progress(offset) {
    if (offset > this.#acknowledged) {
      this.#acknowledged = offset;
      this.#deadline = performance.now() + this.#timing.noProgressTimeoutMs;
      this.#lastInterruption = undefined;
    }
    this.onProgress(offset, this.#image.length);
  }
  async #read(characteristic) { return decodeStatus(await this.#operation(() => characteristic.readValue(), 'reading watch status')); }
  async #wait(channel, predicate, timeoutMs, validate = () => {}, stage = 'waiting for a watch acknowledgement') {
    const deadline = performance.now() + timeoutMs;
    this.#stage = stage;
    while (true) {
      this.#checkRunning();
      if (channel.error) throw channel.error;
      const status = channel.latest;
      if (status) {
        validate(status);
        if (predicate(status)) return status;
      }
      const remaining = Math.min(deadline - performance.now(), this.#remaining(stage));
      if (remaining <= 0) throw new UpdateError(`Timed out while ${stage}. Reconnecting to check the watch’s saved progress…`,
        {retryable: true, code: 'ACK_TIMEOUT', stage});
      const signal = this.#controller.signal;
      await new Promise((resolve, reject) => {
        let timer;
        const cleanup = () => {
          clearTimeout(timer); channel.events.removeEventListener('status', changed);
          signal.removeEventListener('abort', abort);
        };
        const changed = () => { cleanup(); resolve(); };
        const abort = () => { cleanup(); reject(abortError()); };
        channel.events.addEventListener('status', changed, {once: true});
        signal.addEventListener('abort', abort, {once: true});
        timer = setTimeout(changed, remaining);
      });
    }
  }
  async #sendConnected() {
    const server = await this.#operation(() => this.device.gatt.connect(), 'connecting to the watch', this.#timing.connectTimeoutMs);
    this.#state('connecting', 'Finding the watch’s firmware update service.');
    const service = await this.#operation(() => server.getPrimaryService(UUIDS.service), 'finding the update service', this.#timing.connectTimeoutMs);
    const control = await this.#operation(() => service.getCharacteristic(UUIDS.control), 'finding the update control', this.#timing.connectTimeoutMs);
    const data = await this.#operation(() => service.getCharacteristic(UUIDS.data), 'finding the firmware data channel', this.#timing.connectTimeoutMs);
    const statusNative = await this.#operation(() => service.getCharacteristic(UUIDS.status), 'finding the update status', this.#timing.connectTimeoutMs);
    check(typeof control.writeValueWithResponse === 'function' && typeof data.writeValueWithResponse === 'function',
      'This browser cannot perform acknowledged Bluetooth writes. Use a current Chrome or Edge browser.');
    check(typeof statusNative.startNotifications === 'function', 'The watch must support OTA status notifications.');
    const statusCharacteristic = {latest: null, error: null, events: new EventTarget()};
    const changed = event => {
      try { statusCharacteristic.latest = decodeStatus(event.target.value); }
      catch (error) { statusCharacteristic.error = error; }
      statusCharacteristic.events.dispatchEvent(new Event('status'));
    };
    statusNative.addEventListener('characteristicvaluechanged', changed);
    try {
      await this.#operation(() => statusNative.startNotifications(), 'subscribing to watch status');
      let status = await this.#read(statusNative);
      statusCharacteristic.latest = status;
      let window = 1, withoutResponse = false;
      // Protocol2 status is unchanged. Write-only CONTROL on older watches
      // opts out without probing an unsupported ATT read or changing handles.
      if (control.properties?.read) {
        const value = await this.#operation(() => control.readValue(), 'reading transfer capabilities');
        const caps = new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
        check(caps.length === 4 && caps[0] === 1 && caps[1] === 2 && caps[2] === 1 && caps[3] === 0,
          'Unsupported OTA transfer capabilities.');
        window = 2;
        withoutResponse = !!data.properties?.writeWithoutResponse && typeof data.writeValueWithoutResponse === 'function';
      }

      if (status.phase === PHASE.WAITING) {
        check(this.#acknowledged === 0, 'The watch cleared this transfer. Start a new transfer after opening its update screen.');
        check(status.total === 0 && status.offset === 0 && status.session === 0, 'Watch reported an inconsistent waiting state.');
        this.#state('connecting', 'Preparing the watch to receive firmware.');
        await this.#operation(() => control.writeValueWithResponse(packet(1, this.#session, this.#image.length)), 'starting the transfer');
        status = await this.#wait(statusCharacteristic, value => value.phase !== PHASE.WAITING, this.#timing.ackTimeoutMs,
          undefined, 'preparing watch storage');
      }
      this.#identity(status);
      this.#progress(status.offset);
      if (status.phase === PHASE.READY) {
        check(status.offset === this.#image.length, 'Watch verified an incomplete image.');
        return;
      }
      if (status.phase === PHASE.RECEIVING) {
        this.#state('sending', withoutResponse ? 'Sending firmware with accelerated transfer.' :
          window === 2 ? 'Sending firmware with buffered transfer.' : 'Sending firmware to the watch.');
        this.#measure('data');
      }
      let sent = status.offset, outstanding = [];
      while (status.phase === PHASE.RECEIVING && status.offset < status.total) {
        if (statusCharacteristic.error) throw statusCharacteristic.error;
        const next = statusCharacteristic.latest;
        this.#identity(next);
        check(next.phase === PHASE.RECEIVING, 'Watch entered verification before receiving the complete image.');
        if (next.offset > status.offset) {
          check(outstanding.includes(next.offset), 'Watch acknowledgement does not match this chunk boundary.');
          outstanding = outstanding.filter(end => end > next.offset);
          this.#progress(next.offset);
        }
        status = next;
        if (status.offset === status.total) break;
        check(status.chunk > 0, 'Watch has no usable Bluetooth write size. Reconnect and retry.');
        if (outstanding.length < window && sent < status.total) {
          const end = Math.min(status.total, sent + status.chunk);
          const value = new Uint8Array(8 + end - sent);
          const header = new DataView(value.buffer);
          header.setUint32(0, this.#session, true); header.setUint32(4, sent, true);
          value.set(this.#image.subarray(sent, end), 8);
          outstanding.push(end); sent = end;
          // One native operation at a time avoids browser GATT-busy races.
          // The negotiated window bounds packets awaiting flash readback.
          await this.#operation(() => withoutResponse ? data.writeValueWithoutResponse(value) :
            data.writeValueWithResponse(value), 'sending a firmware chunk');
          continue;
        }
        await this.#wait(statusCharacteristic,
          next => next.offset > status.offset || next.phase !== PHASE.RECEIVING,
          this.#timing.ackTimeoutMs, next => this.#identity(next));
      }
      check(status.offset === this.#image.length, 'Watch entered verification before receiving the complete image.');
      this.#measure('verify');
      this.#state('verifying', 'The watch is checking the received firmware.');
      if (status.phase === PHASE.RECEIVING) {
        await this.#operation(() => control.writeValueWithResponse(packet(2, this.#session)), 'requesting firmware verification');
      } else check(status.phase === PHASE.VERIFYING, 'Unexpected watch transfer state.');
      status = await this.#wait(statusCharacteristic, next => next.phase === PHASE.READY, this.#timing.verifyTimeoutMs, next => {
        this.#identity(next);
        check(next.offset === this.#image.length && [PHASE.RECEIVING, PHASE.VERIFYING, PHASE.READY].includes(next.phase),
          'Watch verification does not match this transfer.');
      }, 'verifying the firmware on the watch');
      this.#identity(status);
    } finally {
      this.#measure(null);
      statusNative.removeEventListener('characteristicvaluechanged', changed);
    }
  }
  async run() {
    check(!this.#active, 'A firmware transfer is already running.');
    if (this.#pendingOperations > 0) throw new UpdateError('The previous Bluetooth request is still closing. Wait a moment, then resume.',
      {retryable: true, code: 'BLUETOOTH_CLOSING'});
    this.#active = true;
    this.#started = performance.now(); this.#seconds = {data: 0, verify: 0}; this.#measurePhase = null;
    this.#controller = new AbortController();
    this.#stage = 'connecting to the watch';
    this.#lastInterruption = undefined;
    this.#deadline = performance.now() + this.#timing.noProgressTimeoutMs;
    try {
      while (true) {
        this.#checkRunning();
        this.#remaining(this.#stage);
        this.#state('connecting', 'Connecting to the selected watch.');
        try {
          await this.#sendConnected();
          this.#checkRunning();
          this.#state('ready', 'Firmware verified. Tap INSTALL on the watch, then KEEP after it restarts.');
          return {session: this.#session, bytes: this.#image.length, timings: this.timings};
        } catch (error) {
          this.#disconnect();
          this.#checkRunning();
          if ((error instanceof UpdateError && !error.retryable) || ['NotFoundError', 'NotSupportedError', 'SecurityError', 'TypeError'].includes(error?.name)) throw error;
          const stage = error?.stage || this.#stage;
          this.#stage = stage;
          if (!(error instanceof UpdateError) && error?.message) this.#lastInterruption = error.message;
          const detail = !(error instanceof UpdateError) && error?.message ? ` ${error.message}` : '';
          this.#state('reconnecting', `Connection interrupted while ${stage}.${detail} Reconnecting to the last acknowledged byte.`);
          await this.#drainOperations(stage);
          const remaining = this.#remaining(stage);
          await this.#sleep(Math.min(this.#timing.retryDelayMs, remaining));
        }
      }
    } catch (error) {
      if (this.#controller.signal.aborted) {
        this.#state('stopped', 'Transfer paused. Keep this page open to resume, or Cancel on the watch to discard it.');
        throw abortError();
      }
      throw error;
    } finally {
      this.#active = false;
      this.#disconnect();
    }
  }
}
