const UPLOAD_HEADER_LAYOUTS = [
    {
        format: 'CSV (.csv)',
        headers: 'Artist, Title, Disc, Source, Genre, Video URL, Location',
        notes: 'Comma-separated spreadsheet. Artist and Title are required. Add Source (or Publisher / Provider / Brand), Disc, and Video URL so singers can distinguish versions. Location stays private.',
    },
    {
        format: 'TSV / Delimited Text (.tsv, .txt)',
        headers: 'Artist\tTitle\tDisc\tSource\tGenre\tVideo URL\tLocation',
        notes: 'Tab-, semicolon-, or pipe-delimited text files. Common header aliases (e.g. Song, DiskID, Disc ID, BookID, Tags) are auto-mapped and editable before import; file path columns stay private.',
    },
    {
        format: 'VirtualDJ XML (database.xml)',
        headers: '<Song FilePath="..."><Tags Author="..." Title="..." Genre="..." Label="..." Flag="32" /></Song>',
        notes: 'Standard VirtualDJ database.xml schema. Filters hybrid libraries to karaoke tracks automatically and reads optional Label into Source when present.',
    },
    {
        format: 'Local Folder Scan',
        headers: 'Disc - Artist - Title.zip (or Artist - Title.mp3)',
        notes: 'Scans file names locally in your browser using a selectable naming pattern. Media files are never uploaded.',
    },
]

export function UploadHeadersGuide() {
    return (
        <div style={{ marginTop: 'var(--space-5)', marginBottom: 'var(--space-5)' }}>
            <h3>Suggested Header Layouts for File Uploads</h3>
            <p>
                MicUp detects delimiters and common column headers automatically, and lets you adjust column mappings in the Upload Assistant.
                For <code>.csv</code> / <code>.tsv</code> files, include <code>Source</code> (or <code>Publisher</code> / <code>Provider</code>) and <code>Disc</code> so multi-version songs display clearly in the singer request picker:
            </p>
            <div style={{ overflowX: 'auto', marginTop: 'var(--space-3)' }}>
                <table class="docs-table">
                    <thead>
                        <tr>
                            <th>File Type</th>
                            <th>Suggested Headers / Structure</th>
                            <th>Mapping Notes</th>
                        </tr>
                    </thead>
                    <tbody>
                        {UPLOAD_HEADER_LAYOUTS.map((row) => (
                            <tr key={row.format}>
                                <td><strong>{row.format}</strong></td>
                                <td><code>{row.headers}</code></td>
                                <td>{row.notes}</td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>
        </div>
    )
}
