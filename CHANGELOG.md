# Changelog

This file records all notable changes to this project.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- In the search, singles show in the normal color; only the `single` tag at the right is pale.
- `]` and `[` go to the next and the previous track; the legends show `[ ] track`. `n` `p` and `>` `<` still work.

### Fixed

- scopolamine stopped with the panic "strings: negative Repeat count" when MusicKit reported a negative position during a change of tracks. The position is now never less than zero.

## [0.3.0] - 2026-09-29

### Added

- A local library mode for the music files in a folder: `local_root` in the config file, or `~/Music`. `L` switches between Apple Music and the local library. Each mode keeps its selection, and the music plays on when you switch. `v` sorts the local library by metadata (album artists, albums, tracks; the default) or by folders (top folders, album folders, files). The status bar shows the mode and the format of the track that plays, for example `local · metadata · FLAC 44.1/16`.
- The local player is mpv, through its IPC socket: gapless playback, album ReplayGain, no user configuration and no scripts. The volume is the same for both modes.
- The local scan reads new and changed files with ffprobe: tags, length, codec, sample rate, and bit depth. Missing tags come from the folder names. A cue sheet that splits one audio file into tracks gives separate tracks, played gapless as chapters of the file. The scan runs at the start, after `R`, and 3 seconds after files in the folder change.
- Local covers: the picture in the audio file, else an image in the album folder.
- Local album rows show the format of the album at the right, in a pale color, also on the cursor row: `FLAC 44.1/16` for lossless formats, the bitrate for lossy formats (`MP3 320`), and the average with a tilde for a variable bitrate (`MP3 ~245`). An album with tracks in different formats shows `mixed`. The status bar shows the format of the playing track in the same way. For MP3, the VBR header of the first frame tells VBR from CBR.
- Resume for each mode: the session keeps the selection and the last track and position of both modes, and the mode that showed. `enter` on the resume track of a mode continues at the saved position.
- Chrome and MusicKit start only when the Apple Music view shows for the first time.
- Color themes, as in godoist. `T` opens a theme picker: a move previews the theme on the whole screen, `enter` keeps it and saves it as `theme` in `~/.config/scopolamine/config.json`, and `esc` goes back. The themes are `scopolamine` (the scopolamine colors on the terminal background, the default) and 19 palettes from tideui. They set the terminal background while scopolamine runs. `--theme NAME` selects a theme for one run. The legends show `T theme`.
- A Makefile. `make` builds scopolamine and copies the binary to `~/.local/bin`. `make BINDIR=/other/dir` selects a different directory, and `make test` runs the tests.

### Changed

- The lists sort in dictionary order: case is ignored, and accented letters sort with their base letters (Ärger with the A names).
- The legends show the keys in the accent color of the theme, bold, and their actions in a muted color, as in godoist.
- The "All" rows use the accent color of the theme and stay at the top of their columns, with an empty line above and below them; the list under them scrolls on its own.
- When the key legend does not fit on one line, the items that do not fit start a second line, as in godoist.

### Fixed

- The legends of the library and the search show `+/- vol` again.

## [0.2.0] - 2026-09-29

### Added

- Album covers in kitty and Ghostty. The cover of the album is at the bottom right of the track column, as a square that uses at most half of the column height. The cover shows only for a selected album, not for "All albums". The search shows covers too. scopolamine uses the kitty graphics protocol with Unicode placeholders, and keeps the covers in `~/.cache/scopolamine/art/`. `SCOPOLAMINE_COVERS=0` turns the covers off, and `SCOPOLAMINE_COVERS=1` turns them on in other terminals.

## [0.1.0] - 2026-09-29

The first release.

### Added

Screen and navigation:

- Terminal UI with three columns: artists, albums, and tracks, and a status bar with the track, the progress, the bitrate, and the volume.
- Each column starts with an "All" row. "All artists" shows all the albums. "All albums" shows all the tracks of the artist, grouped by album, with a header for each album.
- Albums in date order. Undated albums come last. "Various Artists" and similar names group the compilations, at the end of the list. A leading "The" does not change the sort order of an artist.
- Track numbers on all tracks, as `1. Title`. Multi-disc albums show `2-3. Title`.
- `tab` and `shift+tab` go to the next or the previous column. `l`, `h`, and `1` `2` `3` also work.
- `enter` on an artist goes to the albums. `enter` on an album plays it and goes to the tracks, with the cursor on the first track.
- A filter for each column (`/`). `tab` and `enter` keep the filter, and `esc` clears it.
- `o` goes to the album that plays.
- A help screen (`?`) with the keys and the version.

Playback:

- Apple Music playback through MusicKit JS in a headless Google Chrome with Widevine, at 256 kbps AAC, the maximum on Linux.
- The full album goes into one MusicKit queue. If the queue fails, playback continues one track at a time.
- `space` plays or pauses. `n` and `p` go to the next or the previous track. `x` stops.
- `←` and `→` seek 10 seconds. With `shift`, they seek 60 seconds. `,` and `.` also work.
- `+` and `-` change the volume. The volume persists.
- Tracks without a stream in the country of the account show as "unavailable". Playback skips them.
- Resume: at the next start, scopolamine selects the artist, album, and track of the last session. `space` continues the last track at the same position.
- MPRIS support for media keys and desktop media widgets.
- A player log in `~/.cache/scopolamine/player.log`.

Library:

- A local library cache in SQLite. The album list syncs at the start when it is older than 12 hours, and with `R` or `scopolamine sync`. The sync gets eight pages at a time and shows the progress.
- The track lists of an album load when you select the album, and stay in the cache.

Search:

- A search of the Apple Music catalog (`s`), with the same three columns. The first artist row shows the albums that match the search. An artist row shows the discography of the artist, oldest first.
- "✓ in library" marks the albums that are in your library.
- `enter` plays an album from the search without an addition to the library.
- `a` adds an album to your library. The album shows in the library view immediately.

Removal:

- `D` removes an album from your library after a confirmation. It works in the library view and in the search.

Sign-in and tokens:

- `scopolamine login` opens music.apple.com in a Chrome window and gets the user token after the sign-in. `scopolamine logout` removes it.
- The developer token comes from `SCOPOLAMINE_DEV_TOKEN`, from the config file, or from the music.apple.com web player. `scopolamine token` shows the source and the expiry date, and `scopolamine token refresh` gets the token again.

Commands:

- `scopolamine`, `--offline`, `login`, `logout`, `sync`, `token`, `version`, and `--version`.

License:

- MIT license. Parts of the code come from vibez by Simone Pelosi (MIT license). NOTICE and THIRD_PARTY_LICENSES.md give the details.

[Unreleased]: https://github.com/biomassa/scopolamine/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/biomassa/scopolamine/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/biomassa/scopolamine/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/biomassa/scopolamine/releases/tag/v0.1.0
