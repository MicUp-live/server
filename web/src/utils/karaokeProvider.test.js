import test from 'node:test'
import assert from 'node:assert/strict'
import { resolveProvider, resolveTrackProvider, extractDiscCode } from './karaokeProvider.js'

test('extractDiscCode finds manufacturer disc codes in filenames and ignores generic tokens', () => {
    assert.equal(extractDiscCode('Queen - Bohemian Rhapsody [SC8101-02]'), 'SC8101-02')
    assert.equal(extractDiscCode('SF045-12 - Queen - Bohemian Rhapsody'), 'SF045-12')
    assert.equal(extractDiscCode('C:\\Karaoke\\KFN-1234 - Dreams.zip'), 'KFN-1234')
    assert.equal(extractDiscCode('Queen - Bohemian Rhapsody (1975)'), null)
    assert.equal(extractDiscCode('THE-01 - School Song'), null)
})

test('resolveProvider prioritizes explicit brand over conflicting disc prefix', () => {
    assert.equal(resolveProvider('SC8101-02', 'KaraFun'), 'KaraFun')
    assert.equal(resolveProvider('TH100-01', 'Sunfly'), 'Sunfly')
})

test('resolveProvider requires digit boundaries on short disc prefixes to avoid false positives', () => {
    assert.equal(resolveProvider('SC8101-02'), 'Sound Choice')
    assert.equal(resolveProvider('SF045-12'), 'Sunfly')
    assert.equal(resolveProvider('KFN-1234'), 'KaraFun')
    assert.equal(resolveProvider('THE-01'), null)
    assert.equal(resolveProvider('SCHOOL-1'), null)
    assert.equal(resolveProvider('SAVE-1'), null)
    assert.equal(resolveProvider('PC-01'), null)
    assert.equal(resolveProvider('CUSTOM-01'), null)
})

test('resolveTrackProvider preserves custom explicit source over disc prefix inference', () => {
    assert.equal(resolveTrackProvider({ source: 'KaraokePro', disc: 'SC8101-02' }), 'KaraokePro')
    assert.equal(resolveTrackProvider({ source: 'karafun', disc: 'SC8101-02' }), 'KaraFun')
    assert.equal(resolveTrackProvider({ disc: 'SC8101-02' }), 'Sound Choice')
    assert.equal(resolveTrackProvider({ disc: 'THE-01' }), '')
    assert.equal(resolveTrackProvider({ canonical_id: 'can_123', title: 'Dreams' }), '')
})

test('resolveProvider and extractDiscCode support multipart, letter-suffixed, and expanded prefixes', () => {
    assert.equal(resolveProvider('CBEP457-3-08'), 'Chartbuster')
    assert.equal(resolveProvider('CBE102-04'), 'Chartbuster')
    assert.equal(resolveProvider('NSC2000-06-08'), 'Sound Choice')
    assert.equal(resolveProvider('VPSC8050-08'), 'Sound Choice')
    assert.equal(resolveProvider('SCDEMO-08'), 'Sound Choice')
    assert.equal(resolveProvider('MM6380B-01'), 'Music Maestro')
    assert.equal(resolveProvider('LG042-05'), 'Legends')
    assert.equal(resolveProvider('KV12345'), 'Karaoke Version')
    assert.equal(extractDiscCode('Cbep457-3-08 - Garth Brooks - Friends'), 'CBEP457-3-08')
    assert.equal(extractDiscCode('NSC2000-06-08 - Shania Twain - Man! I Feel Like a Woman!'), 'NSC2000-06-08')
    assert.equal(extractDiscCode('Queen - Song [Mm6380B-01]'), 'MM6380B-01')
})
