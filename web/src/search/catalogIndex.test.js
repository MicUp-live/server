import test from 'node:test'
import assert from 'node:assert/strict'

import { highlightMatch } from './catalogIndex.js'

test('highlightMatch treats regular expression characters literally', () => {
  assert.deepEqual(highlightMatch('Use [brackets] and C++', '[brackets] C++'), [
    { text: 'Use ', highlight: false },
    { text: '[brackets]', highlight: true },
    { text: ' and ', highlight: false },
    { text: 'C++', highlight: true },
  ])
})

test('highlightMatch handles a backslash literally', () => {
  assert.deepEqual(highlightMatch('AC\\DC', '\\'), [
    { text: 'AC', highlight: false },
    { text: '\\', highlight: true },
    { text: 'DC', highlight: false },
  ])
})
