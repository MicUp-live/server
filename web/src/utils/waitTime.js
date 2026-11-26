export const MINUTES_PER_SONG = 4;

/**
 * Estimates wait time in minutes given the number of songs ahead.
 * For raw estimates > 15 minutes, rounds to the nearest 5-minute increment.
 *
 * @param {number} songsCount - Number of songs in queue ahead
 * @returns {number} Estimated minutes
 */
export function estimateWaitMinutes(songsCount) {
  if (!songsCount || songsCount <= 0) return 0;
  const raw = songsCount * MINUTES_PER_SONG;
  if (raw > 15) {
    return Math.round(raw / 5) * 5;
  }
  return raw;
}

/**
 * Formats estimated wait time into human-friendly string (e.g. '~8m', '~35m', '~1h 15m').
 *
 * @param {number} songsCount - Number of songs in queue ahead
 * @returns {string} Formatted wait time, or '' if <= 0
 */
export function formatWaitEstimate(songsCount) {
  const mins = estimateWaitMinutes(songsCount);
  if (mins <= 0) return '';
  if (mins < 60) {
    return `~${mins}m`;
  }
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  if (m === 0) {
    return `~${h}h`;
  }
  return `~${h}h ${m}m`;
}
