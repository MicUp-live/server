import { test } from 'node:test';
import assert from 'node:assert/strict';
import { estimateWaitMinutes, formatWaitEstimate, MINUTES_PER_SONG } from './waitTime.js';

test('estimateWaitMinutes: returns 0 for non-positive counts', () => {
  assert.equal(estimateWaitMinutes(0), 0);
  assert.equal(estimateWaitMinutes(-1), 0);
  assert.equal(estimateWaitMinutes(null), 0);
  assert.equal(estimateWaitMinutes(undefined), 0);
});

test('estimateWaitMinutes: computes raw minutes when <= 15', () => {
  assert.equal(estimateWaitMinutes(1), 4);
  assert.equal(estimateWaitMinutes(2), 8);
  assert.equal(estimateWaitMinutes(3), 12);
});

test('estimateWaitMinutes: rounds to nearest 5 for raw > 15', () => {
  assert.equal(estimateWaitMinutes(4), 15); // raw 16 -> 15
  assert.equal(estimateWaitMinutes(5), 20); // raw 20 -> 20
  assert.equal(estimateWaitMinutes(6), 25); // raw 24 -> 25
  assert.equal(estimateWaitMinutes(7), 30); // raw 28 -> 30
  assert.equal(estimateWaitMinutes(15), 60); // raw 60 -> 60
  assert.equal(estimateWaitMinutes(18), 70); // raw 72 -> 70
});

test('formatWaitEstimate: formats empty or friendly strings', () => {
  assert.equal(formatWaitEstimate(0), '');
  assert.equal(formatWaitEstimate(-5), '');
  assert.equal(formatWaitEstimate(1), '~4m');
  assert.equal(formatWaitEstimate(2), '~8m');
  assert.equal(formatWaitEstimate(4), '~15m');
  assert.equal(formatWaitEstimate(6), '~25m');
  assert.equal(formatWaitEstimate(15), '~1h');
  assert.equal(formatWaitEstimate(18), '~1h 10m');
  assert.equal(formatWaitEstimate(30), '~2h');
  assert.equal(formatWaitEstimate(31), '~2h 5m');
});
