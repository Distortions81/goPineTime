import test from 'node:test';
import assert from 'node:assert/strict';
import {benchmark, simulate} from '../scripts/ota_simulation.mjs';

test('actual web sender benchmark preserves flash truth, two-slot budget and one native operation', async () => {
  const {profiles} = await benchmark();
  assert.ok(profiles.latency_bound.speedup > 1.9);
  assert.ok(profiles.flash_bound.speedup < 1.1);
  for (const modes of Object.values(profiles)) for (const mode of ['legacy', 'window_response', 'window_wnr']) {
    assert.equal(modes[mode].maxNative, 1); assert.ok(modes[mode].maxQueue <= 2); assert.equal(modes[mode].packets, 200);
  }
  assert.equal(profiles.latency_bound.legacy.maxQueue, 1); assert.equal(profiles.latency_bound.window_wnr.maxQueue, 2);
});
test('missing WNR property or API keeps full-sized response writes', async () => {
  for (const options of [{wnr: false}, {api: false}]) {
    const result = await simulate({size: 1000, ...options});
    assert.equal(result.packets, 5); assert.equal(result.wnrPackets, 0); assert.equal(result.connections, 1);
  }
});
test('cumulative ACK covers a lost earlier notification', async () => {
  const result = await simulate({size: 1000, fault: 'lost_notification'});
  assert.equal(result.connections, 1); assert.ok(!result.progress.includes(236)); assert.equal(result.progress.at(-1), 1000);
});
test('lost packet and transient queue pressure resume from verified flash only', async () => {
  for (const fault of ['lost_packet', 'queue_pressure']) {
    const result = await simulate({size: 1000, fault});
    assert.equal(result.connections, 2); assert.equal(result.progress.at(-1), 1000);
    assert.equal(result.maxNative, 1); assert.ok(result.maxQueue <= 2);
  }
  assert.deepEqual((await simulate({size: 1000, fault: 'queue_pressure'})).offsets, [0, 236, 472, 236, 472, 708, 944]);
});
test('wrong session, unsent boundary, readback failure, reset and malformed capabilities never verify', async () => {
  for (const fault of ['wrong_session', 'bad_boundary', 'readback', 'reset', 'bad_caps']) {
    await assert.rejects(simulate({size: 1000, fault}), error => error.name === 'UpdateError', fault);
  }
});
