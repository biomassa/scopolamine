# Changelog

This file records all notable changes to this project.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-09-30

### Added

- Fades. Before a stop, a pause, or a change of track, the music fades out in 0.2 seconds. After a pause, the music fades in in 0.2 seconds. The new track starts at full volume. A second key push during a fade does its action immediately, without a new fade. Seeks and the change from one track to the next in an album do not fade. This applies to both modes and to the media keys.
- `q` also fades the music out before scopolamine quits. During that fade, scopolamine ignores all keys except `ctrl+c`, which quits immediately. The track and position stay as the resume point.

## [0.4.0] - 2026-09-29

### Changed

- After 10 minutes in the local mode, Chrome and MusicKit stop. This frees approximately 750 MB of memory. They do not stop while Apple Music plays. A paused Apple Music track becomes the resume point of the Apple Music mode. Chrome and MusicKit start again when you switch to the Apple Music mode.
- In the search view, singles show in the normal color. Only the `single` tag at the right is pale.
- `]` and `[` go to the next and the previous track. The legends show `[ ] track`. `n` `p` and `>` `<` also work.

### Fixed

- scopolamine stopped with the panic "strings: negative Repeat count" when MusicKit reported a negative position at a change of track. Now the position is never less than zero.

## [0.3.0] - 2026-09-29

### Added

- A local mode for the music files in a folder. The folder is `local_root` in the config file, or `~/Music`. `L` switches between the Apple Music mode and the local mode. Each mode keeps its selection, and the music continues when you switch.
- `v` sorts the local mode by metadata or by folders. With metadata (the default), the columns are album artists, albums, and tracks. With folders, the columns are top folders, album folders, and files.
- The status bar shows the mode and the format of the track that plays, for example `local · metadata · FLAC 44.1/16`.
- The local player is mpv. scopolamine controls mpv through its IPC socket. mpv plays without gaps and applies the album ReplayGain. It runs without the user configuration and without scripts. The volume is the same for both modes.
- The local scan reads new and changed files with ffprobe: the tags, the length, the codec, the sample rate, and the bit depth. When a tag is not in the file, scopolamine uses the folder names.
- A cue sheet that divides one audio file into tracks gives separate tracks. mpv plays them without gaps, as chapters of the file.
- The local scan runs at the start, after `R`, and 3 seconds after a change of files in the folder.
- Local covers: the picture in the audio file, else an image in the album folder.
- Local album rows show the format of the album at the right, in a pale color, also on the cursor row. Lossless formats show the sample rate and the bit depth (`FLAC 44.1/16`). Lossy formats show the bitrate (`MP3 320`). A variable bitrate shows the average with a tilde (`MP3 ~245`). An album with tracks in different formats shows `mixed`.
- The status bar shows the format of the track that plays in the same way. For MP3, the VBR header of the first frame tells VBR from CBR.
- Resume for each mode. The session keeps the selection, the last track, and the position of both modes. It also keeps the mode that shows. `enter` on the resume track of a mode continues at the saved position.
- Chrome and MusicKit start only when the Apple Music mode shows for the first time.
- Color themes, as in godoist. `T` opens a theme picker. When you move through the list, the whole screen shows the theme. `enter` keeps the theme and saves it as `theme` in `~/.config/scopolamine/config.json`. `esc` returns to the previous theme.
- The themes are `scopolamine` (the default) and 19 palettes from tideui. The `scopolamine` theme uses the scopolamine colors on the terminal background. The other themes set the terminal background while scopolamine runs. `--theme NAME` selects a theme for one run. The legends show `T theme`.
- A Makefile. `make` builds scopolamine and copies the binary to `~/.local/bin`. `make BINDIR=/other/dir` selects a different directory. `make test` runs the tests.

### Changed

- The lists use dictionary order. The sort ignores case, and accented letters sort with their base letters ("Ärger" sorts with the A names).
- The legends show the keys in the accent color of the theme, in bold. They show the actions in a muted color, as in godoist.
- The "All" rows use the accent color of the theme. They stay at the top of their columns, with an empty line above and below them. The list under them scrolls independently.
- When the key legend is too long for one line, the items that do not fit go to a second line, as in godoist.

### Fixed

- The legends of the library view and the search view show `+/- vol` again.

## [0.2.0] - 2026-09-29

### Added

- Album covers in kitty and Ghostty. The cover shows at the bottom right of the track column, as a square that uses a maximum of half of the column height.
- The cover shows only for a selected album, not for "All albums". The search view also shows covers.
- scopolamine uses the kitty graphics protocol with Unicode placeholders. It keeps the covers in `~/.cache/scopolamine/art/`.
- With `SCOPOLAMINE_COVERS=0`, scopolamine shows no covers. With `SCOPOLAMINE_COVERS=1`, scopolamine shows covers also in other terminals.

## [0.1.0] - 2026-09-29

The first release.

### Added

Screen and navigation:

- A terminal UI with three columns: artists, albums, and tracks. A status bar shows the track, the progress, the bitrate, and the volume.
- Each column starts with an "All" row. "All artists" shows all the albums. "All albums" shows all the tracks of the artist, in groups by album, with a header for each album.
- Albums in date order. Albums without a date come last. "Various Artists" and similar names group the compilations at the end of the list. A leading "The" does not change the sort order of an artist.
- Track numbers on all tracks, as `1. Title`. Albums with more than one disc show `2-3. Title`.
- `tab` and `shift+tab` go to the next or the previous column. `l`, `h`, and `1` `2` `3` also work.
- `enter` on an artist goes to the albums. `enter` on an album plays it and goes to the tracks, with the cursor on the first track.
- A filter for each column (`/`). `tab` and `enter` keep the filter. `esc` clears it.
- `o` goes to the album that plays.
- A help screen (`?`) with the keys and the version.

Playback:

- Apple Music playback through MusicKit JS in a headless Google Chrome with Widevine. The quality is 256 kbps AAC, the maximum on Linux.
- The full album goes into one MusicKit queue. If the queue fails, playback continues with one track at a time.
- `space` plays or pauses. `n` and `p` go to the next or the previous track. `x` stops.
- `←` and `→` seek 10 seconds. With `shift`, they seek 60 seconds. `,` and `.` also work.
- `+` and `-` change the volume. scopolamine saves the volume.
- Tracks without a stream in the country of the account show as "unavailable". Playback skips them.
- Resume: at the next start, scopolamine selects the artist, album, and track of the last session. `space` continues the last track at the same position.
- MPRIS support for media keys and desktop media widgets.
- A player log in `~/.cache/scopolamine/player.log`.

Library:

- A library cache in SQLite. The album list syncs at the start when it is older than 12 hours. It also syncs with `R` or `scopolamine sync`. The sync gets eight pages at a time and shows the progress.
- The track list of an album loads when you select the album. It stays in the cache.

Search:

- A search of the Apple Music catalog (`s`), with the same three columns. The first artist row shows the albums that match the search. An artist row shows the discography of the artist, oldest first.
- "✓ in library" marks the albums that are in your library.
- `enter` plays an album from the search. It does not add the album to the library.
- `a` adds an album to your library. The album shows in the library view immediately.

Removal:

- `D` removes an album from your library after a confirmation. It works in the library view and in the search view.

Sign-in and tokens:

- `scopolamine login` opens music.apple.com in a Chrome window. After the sign-in, it gets the user token. `scopolamine logout` removes the user token.
- The developer token comes from `SCOPOLAMINE_DEV_TOKEN`, from the config file, or from the music.apple.com web player. `scopolamine token` shows the source and the expiry date. `scopolamine token refresh` gets the token again.

Commands:

- `scopolamine`, `--offline`, `login`, `logout`, `sync`, `token`, `version`, and `--version`.

License:

- MIT license. Parts of the code come from vibez by Simone Pelosi (MIT license). NOTICE and THIRD_PARTY_LICENSES.md give the details.

[Unreleased]: https://github.com/biomassa/scopolamine/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/biomassa/scopolamine/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/biomassa/scopolamine/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/biomassa/scopolamine/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/biomassa/scopolamine/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/biomassa/scopolamine/releases/tag/v0.1.0
