package library

import (
	"context"
	"crypto/sha1" //nolint:gosec // ids only
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// FolderPrefix marks folder-mode album ids: FolderPrefix + the album folder
// (relative to the library root).
const FolderPrefix = "dir:"

// FileStamp identifies a version of a file.
type FileStamp struct {
	Mtime int64 // Unix nanoseconds
	Size  int64
}

// LocalFile is the scan result for one file: an audio file, or a cue sheet
// that splits one audio file into tracks.
type LocalFile struct {
	Path   string
	Stamp  FileStamp
	Albums []Album // the albums of the tracks (usually one)
	Tracks []Track
}

// LocalAlbumID is the id of a local album: album artist, album title, and
// album folder make one album.
func LocalAlbumID(albumArtist, album, folder string) string {
	sum := sha1.Sum([]byte(strings.ToLower(albumArtist) + "\x00" + strings.ToLower(album) + "\x00" + folder)) //nolint:gosec // id
	return "local:" + hex.EncodeToString(sum[:10])
}

// LocalTrackID is the id of a local track: its file, and its cue track
// number for a cue-split file.
func LocalTrackID(path string, cueTrack int) string {
	if cueTrack > 0 {
		return "file:" + path + "#" + strconv.Itoa(cueTrack)
	}
	return "file:" + path
}

// LocalFiles returns the stamps of the files that the last scan recorded.
func (s *Store) LocalFiles(ctx context.Context) (map[string]FileStamp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, mtime, size FROM local_files`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]FileStamp{}
	for rows.Next() {
		var p string
		var st FileStamp
		if err := rows.Scan(&p, &st.Mtime, &st.Size); err != nil {
			return nil, err
		}
		out[p] = st
	}
	return out, rows.Err()
}

// UpdateLocal applies a scan: the tracks of changed files replace their old
// tracks, and removed files lose theirs. Albums without tracks are deleted,
// and the track counts are updated.
func (s *Store) UpdateLocal(ctx context.Context, changed []LocalFile, removed []string) error {
	if len(changed) == 0 && len(removed) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	forget := func(path string) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tracks WHERE path = ? AND album_id IN (SELECT id FROM albums WHERE source = ?)`, path, SourceLocal); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM local_files WHERE path = ?`, path)
		return err
	}
	for _, p := range removed {
		if err := forget(p); err != nil {
			return err
		}
	}
	upAlbum, err := tx.PrepareContext(ctx, upsertAlbumSQL)
	if err != nil {
		return err
	}
	insTrack, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO tracks (`+trackCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for _, f := range changed {
		if err := forget(f.Path); err != nil {
			return err
		}
		// A cue sheet claims its audio file: drop the tracks of the file.
		for _, t := range f.Tracks {
			if t.Path != f.Path {
				if _, err := tx.ExecContext(ctx, `DELETE FROM tracks WHERE path = ? AND cue_track = 0 AND album_id IN (SELECT id FROM albums WHERE source = ?)`, t.Path, SourceLocal); err != nil {
					return err
				}
			}
		}
		for _, a := range f.Albums {
			if _, err := upAlbum.ExecContext(ctx, albumArgs(SourceLocal, a)...); err != nil {
				return fmt.Errorf("library: local album %s: %w", a.ID, err)
			}
		}
		for _, t := range f.Tracks {
			// Local tracks are known from the scan; for a cue sheet, the
			// row's path is the audio file, so key the file stamp by f.Path.
			if _, err := insTrack.ExecContext(ctx, trackArgs(t)...); err != nil {
				return fmt.Errorf("library: local track %s: %w", t.ID, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO local_files (path, mtime, size) VALUES (?,?,?)`,
			f.Path, f.Stamp.Mtime, f.Stamp.Size); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`DELETE FROM albums WHERE source = 'local' AND NOT EXISTS (SELECT 1 FROM tracks t WHERE t.album_id = albums.id)`,
		`UPDATE albums SET track_count = (SELECT COUNT(*) FROM tracks t WHERE t.album_id = albums.id), tracks_synced_at = 1 WHERE source = 'local'`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FolderArtists lists the top-level folders of the local library, with the
// number of album folders in each (folder mode).
func (s *Store) FolderArtists(ctx context.Context) ([]Artist, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT top, COUNT(DISTINCT folder) FROM (
	SELECT CASE WHEN instr(t.folder, '/') > 0 THEN substr(t.folder, 1, instr(t.folder, '/') - 1) ELSE t.folder END AS top, t.folder
	FROM tracks t JOIN albums a ON a.id = t.album_id WHERE a.source = 'local')
GROUP BY top ORDER BY lower(top)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Artist
	for rows.Next() {
		var a Artist
		if err := rows.Scan(&a.Name, &a.AlbumCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// FolderAlbums lists the album folders under a top-level folder, or all of
// them when top is "" (folder mode). The title is the folder below top; the
// cover and the year come from its tracks' albums.
func (s *Store) FolderAlbums(ctx context.Context, top string) ([]Album, error) {
	q := `
SELECT t.folder, MIN(a.year), MAX(a.artwork_url), COUNT(*)
FROM tracks t JOIN albums a ON a.id = t.album_id
WHERE a.source = 'local' AND (? = '' OR t.folder = ? OR t.folder LIKE ? ESCAPE '\')
GROUP BY t.folder ORDER BY lower(t.folder)`
	like := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(top) + "/%"
	rows, err := s.db.QueryContext(ctx, q, top, top, like)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Album
	for rows.Next() {
		var folder, art string
		var year sql.NullInt64
		var n int
		if err := rows.Scan(&folder, &year, &art, &n); err != nil {
			return nil, err
		}
		topName, title := folder, folder
		if i := strings.IndexByte(folder, '/'); i >= 0 {
			topName, title = folder[:i], folder[i+1:]
		}
		out = append(out, Album{
			ID: FolderPrefix + folder, Source: SourceLocal, Title: title, Artist: topName,
			Year: int(year.Int64), TrackCount: n, ArtworkURL: art,
		})
	}
	return out, rows.Err()
}

// FolderTracks lists the tracks of an album folder in file order (folder
// mode). Each track's AlbumID is its metadata album.
func (s *Store) FolderTracks(ctx context.Context, folder string) ([]Track, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixCols("t.", trackCols)+`
FROM tracks t JOIN albums a ON a.id = t.album_id
WHERE a.source = 'local' AND t.folder = ? ORDER BY t.path, t.start_ms`, folder)
	if err != nil {
		return nil, err
	}
	return scanTracks(rows)
}

// LocalTrack returns a local track by id.
func (s *Store) LocalTrack(ctx context.Context, id string) (Track, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixCols("t.", trackCols)+`
FROM tracks t JOIN albums a ON a.id = t.album_id WHERE a.source = 'local' AND t.id = ?`, id)
	if err != nil {
		return Track{}, err
	}
	ts, err := scanTracks(rows)
	if err != nil {
		return Track{}, err
	}
	if len(ts) == 0 {
		return Track{}, sql.ErrNoRows
	}
	return ts[0], nil
}

func prefixCols(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}
