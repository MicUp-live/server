import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { getSongbookPermissions, filterHostableSongbooks } from './songbookPermissions.js'
import { resolveDirectQueueHandoff } from '../pages/kj/hooks/useStartShowHandoff.js'

describe('songbookPermissions', () => {
    it('defaults legacy songbooks without permissions object to owner', () => {
        const p = getSongbookPermissions({ id: 'sb-1', name: 'Legacy' })
        assert.equal(p.isOwner, true)
        assert.equal(p.canEditCatalog, true)
        assert.equal(p.canHostShows, true)
        assert.equal(p.canManageBook, true)
        assert.equal(p.canManageAccess, true)
    })

    it('maps collaborator permissions accurately', () => {
        const editorOnly = getSongbookPermissions({
            id: 'sb-2',
            permissions: { is_owner: false, can_edit_catalog: true, can_host_shows: false, can_manage_book: false, can_manage_access: false },
        })
        assert.equal(editorOnly.isOwner, false)
        assert.equal(editorOnly.canEditCatalog, true)
        assert.equal(editorOnly.canHostShows, false)
        assert.equal(editorOnly.canManageAccess, false)

        const hostOnly = getSongbookPermissions({
            id: 'sb-3',
            permissions: { is_owner: false, can_edit_catalog: false, can_host_shows: true, can_manage_book: false, can_manage_access: false },
        })
        assert.equal(hostOnly.canEditCatalog, false)
        assert.equal(hostOnly.canHostShows, true)
    })

    it('filters show picker to books with can_host_shows', () => {
        const books = [
            { id: 'owner-book', permissions: { is_owner: true, can_edit_catalog: true, can_host_shows: true } },
            { id: 'edit-only', permissions: { is_owner: false, can_edit_catalog: true, can_host_shows: false } },
            { id: 'host-only', permissions: { is_owner: false, can_edit_catalog: false, can_host_shows: true } },
        ]
        const hostable = filterHostableSongbooks(books)
        assert.deepEqual(hostable.map(b => b.id), ['owner-book', 'host-only'])
    })

    it('does not open show creation modal on direct link for edit-only songbook', () => {
        const books = [
            { id: 'edit-only', permissions: { is_owner: false, can_edit_catalog: true, can_host_shows: false } },
            { id: 'host-only', permissions: { is_owner: false, can_edit_catalog: false, can_host_shows: true } },
        ]
        const editRes = resolveDirectQueueHandoff('edit-only', [], books)
        assert.equal(editRes.action, 'none')

        const hostRes = resolveDirectQueueHandoff('host-only', [], books)
        assert.equal(hostRes.action, 'open_create')
        assert.equal(hostRes.songbookId, 'host-only')
    })
})
