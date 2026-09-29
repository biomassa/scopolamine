// Package library is the local, source-agnostic music library cache. Apple
// Music albums are synced into it today; local files will land in the same
// tables later with a different Source.
package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Source identifies where an album lives.
const (
	SourceApple = "apple"
)

// VariousArtists is the display name compilation albums are grouped under.
const VariousArtists = "Various Artists"

// Artist is an album artist with at least one album in the library.
type Artist struct {
	Name       string
	AlbumCount int
}

// Album is a library album.
type Album struct {
	ID          string
	Source      string
	CatalogID   string
	Title       string
	Artist      string
	Year        int
	ReleaseDate string
	DateAdded   string
	TrackCount  int
	Genre       string
	ArtworkURL  string
}

// Track is one song of an album.
type Track struct {
	ID        string
	AlbumID   string
	CatalogID string
	Title     string
	Artist    string
	Disc      int
	Number    int
	Duration  time.Duration
	Playable  bool
}

// Store is the SQLite-backed library.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS albums (
	id           TEXT PRIMARY KEY,
	source       TEXT NOT NULL,
	catalog_id   TEXT NOT NULL DEFAULT '',
	title        TEXT NOT NULL,
	artist       TEXT NOT NULL,
	artist_key   TEXT NOT NULL,
	year         INTEGER NOT NULL DEFAULT 0,
	release_date TEXT NOT NULL DEFAULT '',
	date_added   TEXT NOT NULL DEFAULT '',
	track_count  INTEGER NOT NULL DEFAULT 0,
	genre        TEXT NOT NULL DEFAULT '',
	artwork_url  TEXT NOT NULL DEFAULT '',
	tracks_synced_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS albums_artist ON albums(artist_key);
CREATE TABLE IF NOT EXISTS tracks (
	id          TEXT NOT NULL,
	album_id    TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
	catalog_id  TEXT NOT NULL DEFAULT '',
	title       TEXT NOT NULL,
	artist      TEXT NOT NULL,
	disc        INTEGER NOT NULL DEFAULT 0,
	number      INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	playable    INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY (album_id, id)
);
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// DefaultPath is where the library database lives under cacheDir.
func DefaultPath(cacheDir string) string { return filepath.Join(cacheDir, "library.db") }

// Open opens (creating if needed) the database at path. Use ":memory:" in tests.
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("library: %w", err)
		}
		dsn = "file:" + path
	}
	db, err := sql.Open("sqlite", dsn+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("library: open: %w", err)
	}
	db.SetMaxOpenConns(1) // one writer; also keeps ":memory:" a single database
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("library: schema: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("library: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate upgrades databases created by older versions.
func migrate(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('tracks') WHERE name = 'playable'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		// Track caches from before the column existed don't know which
		// tracks are streamable; drop them so they are fetched again.
		for _, q := range []string{
			`ALTER TABLE tracks ADD COLUMN playable INTEGER NOT NULL DEFAULT 1`,
			`DELETE FROM tracks`,
			`UPDATE albums SET tracks_synced_at = 0`,
		} {
			if _, err := db.Exec(q); err != nil {
				return err
			}
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// ArtistKey normalises an album artist for grouping and sorting: case-folded,
// leading "The " dropped, compilations collapsed onto VariousArtists.
func ArtistKey(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	if isVarious(k) {
		return "￿" // sorts compilations last
	}
	k = strings.TrimPrefix(k, "the ")
	return k
}

func isVarious(lower string) bool {
	switch lower {
	case "various artists", "various", "va", "":
		return true
	}
	return false
}

// DisplayArtist maps compilation spellings onto VariousArtists.
func DisplayArtist(name string) string {
	if isVarious(strings.ToLower(strings.TrimSpace(name))) {
		return VariousArtists
	}
	return strings.TrimSpace(name)
}

// YearOf extracts the year from "YYYY-MM-DD" / "YYYY".
func YearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return y
}

const upsertAlbumSQL = `
INSERT INTO albums (id, source, catalog_id, title, artist, artist_key, year, release_date, date_added, track_count, genre, artwork_url)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
	catalog_id=excluded.catalog_id, title=excluded.title, artist=excluded.artist,
	artist_key=excluded.artist_key, year=excluded.year, release_date=excluded.release_date,
	date_added=excluded.date_added, genre=excluded.genre, artwork_url=excluded.artwork_url,
	tracks_synced_at = CASE WHEN albums.track_count = excluded.track_count THEN albums.tracks_synced_at ELSE 0 END,
	track_count=excluded.track_count`

func albumArgs(source string, a Album) []any {
	artist := DisplayArtist(a.Artist)
	year := a.Year
	if year == 0 {
		year = YearOf(a.ReleaseDate)
	}
	return []any{a.ID, source, a.CatalogID, a.Title, artist, ArtistKey(artist),
		year, a.ReleaseDate, a.DateAdded, a.TrackCount, a.Genre, a.ArtworkURL}
}

// UpsertAlbum inserts or updates one album without touching the others
// (e.g. right after adding it to the library).
func (s *Store) UpsertAlbum(ctx context.Context, source string, a Album) error {
	if _, err := s.db.ExecContext(ctx, upsertAlbumSQL, albumArgs(source, a)...); err != nil {
		return fmt.Errorf("library: upsert %s: %w", a.ID, err)
	}
	return nil
}

// DeleteAlbum removes one album and its cached tracks.
func (s *Store) DeleteAlbum(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM albums WHERE id = ?`, id)
	return err
}

// ReplaceAlbums makes the albums of source exactly albums: upserts every one
// and deletes the rest (tracks cascade). Cached tracks of albums that remain
// are kept.
func (s *Store) ReplaceAlbums(ctx context.Context, source string, albums []Album) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS keep (id TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM keep`); err != nil {
		return err
	}
	up, err := tx.PrepareContext(ctx, upsertAlbumSQL)
	if err != nil {
		return err
	}
	keep, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO keep (id) VALUES (?)`)
	if err != nil {
		return err
	}
	for _, a := range albums {
		if _, err := up.ExecContext(ctx, albumArgs(source, a)...); err != nil {
			return fmt.Errorf("library: upsert %s: %w", a.ID, err)
		}
		if _, err := keep.ExecContext(ctx, a.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM albums WHERE source = ? AND id NOT IN (SELECT id FROM keep)`, source); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		"synced_at:"+source, strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		return err
	}
	return tx.Commit()
}

// LastSync reports when source's album list was last replaced (zero if never).
func (s *Store) LastSync(ctx context.Context, source string) time.Time {
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, "synced_at:"+source).Scan(&v); err != nil {
		return time.Time{}
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return time.Unix(n, 0)
}

// Artists lists album artists alphabetically (compilations last).
func (s *Store) Artists(ctx context.Context) ([]Artist, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT MIN(artist), COUNT(*) FROM albums GROUP BY artist_key ORDER BY artist_key`)
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

const albumCols = `id, source, catalog_id, title, artist, year, release_date, date_added, track_count, genre, artwork_url`

func scanAlbums(rows *sql.Rows) ([]Album, error) {
	defer func() { _ = rows.Close() }()
	var out []Album
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.Source, &a.CatalogID, &a.Title, &a.Artist, &a.Year,
			&a.ReleaseDate, &a.DateAdded, &a.TrackCount, &a.Genre, &a.ArtworkURL); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AlbumsByArtist lists an artist's albums, oldest first. artist is matched by
// ArtistKey, so any spelling from Artists works.
func (s *Store) AlbumsByArtist(ctx context.Context, artist string) ([]Album, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+albumCols+` FROM albums WHERE artist_key = ?
ORDER BY CASE WHEN year = 0 THEN 1 ELSE 0 END, year, release_date, title COLLATE NOCASE`, ArtistKey(artist))
	if err != nil {
		return nil, err
	}
	return scanAlbums(rows)
}

// AllAlbums lists every album, by artist then year.
func (s *Store) AllAlbums(ctx context.Context) ([]Album, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+albumCols+` FROM albums
ORDER BY artist_key, CASE WHEN year = 0 THEN 1 ELSE 0 END, year, title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	return scanAlbums(rows)
}

// Album fetches one album.
func (s *Store) Album(ctx context.Context, id string) (Album, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+albumCols+` FROM albums WHERE id = ?`, id)
	if err != nil {
		return Album{}, err
	}
	as, err := scanAlbums(rows)
	if err != nil {
		return Album{}, err
	}
	if len(as) == 0 {
		return Album{}, sql.ErrNoRows
	}
	return as[0], nil
}

// Tracks returns an album's cached tracks in disc/track order. synced is false
// when the tracks were never fetched (or the album changed since).
func (s *Store) Tracks(ctx context.Context, albumID string) (tracks []Track, synced bool, err error) {
	var at int64
	if err := s.db.QueryRowContext(ctx, `SELECT tracks_synced_at FROM albums WHERE id = ?`, albumID).Scan(&at); err != nil {
		return nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, album_id, catalog_id, title, artist, disc, number, duration_ms, playable FROM tracks
WHERE album_id = ? ORDER BY disc, number, title COLLATE NOCASE`, albumID)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var t Track
		var ms int64
		if err := rows.Scan(&t.ID, &t.AlbumID, &t.CatalogID, &t.Title, &t.Artist, &t.Disc, &t.Number, &ms, &t.Playable); err != nil {
			return nil, false, err
		}
		t.Duration = time.Duration(ms) * time.Millisecond
		tracks = append(tracks, t)
	}
	return tracks, at != 0, rows.Err()
}

// SetTracks replaces an album's cached tracks and marks them synced.
func (s *Store) SetTracks(ctx context.Context, albumID string, tracks []Track) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM tracks WHERE album_id = ?`, albumID); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO tracks
(id, album_id, catalog_id, title, artist, disc, number, duration_ms, playable) VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	// Apple occasionally omits disc numbers on single-disc albums.
	sorted := append([]Track(nil), tracks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Disc != sorted[j].Disc {
			return sorted[i].Disc < sorted[j].Disc
		}
		return sorted[i].Number < sorted[j].Number
	})
	for _, t := range sorted {
		if _, err := ins.ExecContext(ctx, t.ID, albumID, t.CatalogID, t.Title, t.Artist, t.Disc, t.Number, t.Duration.Milliseconds(), t.Playable); err != nil {
			return fmt.Errorf("library: track %s: %w", t.ID, err)
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE albums SET tracks_synced_at = ? WHERE id = ?`, time.Now().Unix(), albumID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("library: unknown album %s", albumID)
	}
	return tx.Commit()
}
