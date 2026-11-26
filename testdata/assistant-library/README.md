# Assistant Import Fixture

These are filename-only fixtures for manually testing Assistant Import. Their
contents are intentionally not playable media: the importer sends file name,
relative path, and size only.

Use `full-library` for a complete Assistant Sync test. Select `Artist - Title`,
then drag that directory into Assistant Import. It contains one consistent
naming convention, so a repeat sync does not cause a soft-prune warning.

The `artist-title`, `title-artist`, and `disc-artist-title` directories are
parser-only fixtures. The current UI applies one naming pattern to a complete
sync, not per directory. Test those directories in separate temporary
songbooks, or expect a soft-prune warning when switching between them.

The filenames cover supported extensions, bracket and parenthetical cleanup,
underscores, and a non-music text file that the browser scanner must ignore.
