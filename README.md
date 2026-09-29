# scopolamine

scopolamine is a terminal program for [Apple Music](https://music.apple.com) and local music files on Linux, in Go. It shows your library as three columns: artists, albums, and tracks. It plays full albums. It has no radio, no recommendations, and no playlists.

> This software was developed with the assistance of a LLM.

![scopolamine: the artist, album, and track columns, with an album that plays](docs/screenshot.png)

## Features

- Three columns: artists, albums, and tracks. Each column starts with an "All" row. "All albums" shows all the tracks of an artist, grouped by album.
- Albums in date order, and track numbers on all tracks.
- Full albums play as one queue, so that the tracks follow each other without a new start for each track.
- Album covers at the bottom of the track column, in kitty and Ghostty.
- 20 color themes with a live preview (`T`), as in godoist.
- A filter for each column (`/`).
- A local library mode (`L`) for the music files in a folder, sorted by metadata or by folders (`v`), played gapless with mpv.
- A search of the Apple Music catalog (`s`), with the discography of each artist. A mark shows the albums that are in your library. You can play an album from the search, add it to your library (`a`), or remove it (`D`).
- Tracks that are not available in your country show as "unavailable". Playback skips them.
- Resume: at the next start, scopolamine shows the artist, album, and track of the last session, for each mode. `space` continues the last track at the same position.
- A local library cache in SQLite. The program starts at once and syncs in the background.
- Media keys and desktop media widgets through MPRIS.

See the [changelog](CHANGELOG.md) for more details.

## Requirements

- Linux on x86-64. On arm64, a Chromium with the Widevine module is necessary, because Google supplies no Chrome for arm64 Linux.
- An Apple Music subscription.
- Go 1.27 or later, to build from the source.
- About 140 MB of disk space for a private copy of Google Chrome. scopolamine downloads it at the first start.
- A terminal with true color. For album covers: kitty or Ghostty.
- PipeWire or PulseAudio for the audio output.
- For the local library: mpv and FFmpeg (ffprobe and ffmpeg).

## Installation

```sh
go install -trimpath -ldflags="-s -w" github.com/biomassa/scopolamine/cmd/scopolamine@latest
```

Or build from a clone:

```sh
git clone https://github.com/biomassa/scopolamine
cd scopolamine
go build -trimpath -ldflags="-s -w" -o scopolamine ./cmd/scopolamine
```

`make` does the same build and copies the binary to `~/.local/bin`. `make BINDIR=/other/dir` selects a different directory. `make test` runs the tests.

## Sign-in

```sh
scopolamine login
```

1. scopolamine downloads its copy of Google Chrome, if necessary.
2. A Chrome window opens music.apple.com.
3. Click **Sign In** and sign in with your Apple ID.
4. The window closes when scopolamine finds your sign-in. The terminal shows "Signed in to Apple Music".

Then start the program:

```sh
scopolamine
```

The first start syncs your album list. A large library takes some time. The status line shows the progress.

To sign in with a different account, use `scopolamine logout` and then `scopolamine login`.

## Commands

| Command | Action |
|---|---|
| `scopolamine` | Start the TUI. |
| `scopolamine --offline` | Browse the cache. No player and no sync. |
| `scopolamine --theme NAME` | Use a color theme for this run only. |
| `scopolamine login` | Sign in to Apple Music. |
| `scopolamine logout` | Remove the saved user token. |
| `scopolamine sync` | Sync the album list and show the progress. |
| `scopolamine token` | Show the source and the expiry date of the developer token. |
| `scopolamine token refresh` | Get the developer token again. |
| `scopolamine --version` | Show the version. |

## TUI

### Keys

| Key | Action |
|---|---|
| `tab`, `shift+tab` | Go to the next or the previous column. `l`, `h`, and `1` `2` `3` also work. |
| `j` `k`, `↓` `↑` | Move the cursor. |
| `g` `G`, `pgup` `pgdn` | Go to the top, the bottom, or one half page. |
| `/` | Filter the column. `esc` clears the filter. |
| `enter` | On an artist: go to the albums. On an album: play it and go to the tracks. On a track: play from this track. On the resume track of a mode: continue at the saved position. |
| `space` | Play or pause. After a restart: continue the last track. |
| `←` `→` | Seek 10 seconds back or forward. `shift` seeks 60 seconds. `,` and `.` also work. |
| `]` `[` | Play the next or the previous track. `n` `p` and `>` `<` also work. |
| `+` `-` | Change the volume. |
| `x` | Stop. |
| `o` | Go to the album that plays. |
| `L` | Switch between Apple Music and the local library. |
| `v` | Local library: sort by metadata or by folders. |
| `s` | Search Apple Music. Not in the local library. |
| `D` | Remove the album from your Apple Music library. scopolamine asks first. Not in the local library. |
| `R` | Sync the Apple Music album list, or scan the local folder. |
| `T` | Choose a color theme. |
| `?` | Show the help and the version. |
| `q` | Quit. |

### "All" rows

The "All" rows are in the accent color of the theme and stay at the top of their columns; the list under them scrolls. "All artists" shows all the albums. "All albums" shows all the tracks of the selected artist, grouped by album. `enter` on "All albums" or on "All" plays all these tracks. When "All artists" is selected, "All albums" shows no tracks, because that needs a request for each album in the library.

### Search

`s` opens the search. Type a search text. The search starts when you stop typing, or when you push `enter`.

- **Artists**: the first row, "Matching albums", shows the albums that match the search. An artist row shows all the albums of that artist, oldest first.
- **Albums**: "✓ in library" marks the albums that are in your library.
- `enter` plays an album. It does not add the album to your library.
- `a` adds the album to your library. The album shows in the library view immediately.
- `D` removes an album that has the "✓ in library" mark.
- `s` or `/` changes the search text. `esc` goes back to the library.

The search uses the catalog of your country only.

### Remove an album

`D` removes an album from your Apple Music library. The status line asks for a confirmation. Push `y` to remove the album. Any other key cancels.

- In the album column, `D` removes the selected album.
- In the track column, `D` removes the album of the selected row.
- `D` does not remove "All albums" or an artist.

### Local library

`L` switches between Apple Music and the local library. Each mode keeps its selection, its column, and its sorting. The music plays on when you switch. When you start music in the other mode, the music of the first mode stops. The first mode keeps its track and position: with the cursor on that track, `enter` continues at the position.

The local library is the folder in `local_root` in `~/.config/scopolamine/config.json`. If `local_root` is empty, it is `$XDG_MUSIC_DIR`, else `~/Music`. scopolamine never writes to your music files.

- scopolamine scans the folder at each start, after `R`, and 3 seconds after files in the folder change. The scan reads only new and changed files, with ffprobe: the tags, the length, the codec, the sample rate, and the bit depth.
- All file types that mpv plays count as audio: FLAC, MP3, M4A (AAC and ALAC), Ogg, Opus, WAV, AIFF, WavPack, APE, DSF, and others.
- When tags are missing, scopolamine uses the folder names. The album artist is the folder above the album folder, the album is the album folder, and the title is the file name.
- One album is the album artist, the album title, and the album folder. So discs in different folders stay different albums.
- A cue sheet with one audio file and two or more tracks splits the file into tracks. The file plays as one file with chapters, so the album stays gapless. Cue sheets for albums that are already split into track files are not used. scopolamine reads cue sheets as UTF-8, else as Windows-1252.
- The cover is the picture in the audio file, else an image in the album folder: cover, folder, front, or album (.jpg or .png), else the only image in the folder.

Each local album row shows the format of the album at the right, in a pale color. For a lossless format, it shows the codec, the sample rate in kHz, and the bits: `FLAC 44.1/16`. For a lossy format, it shows the codec and the bitrate in kbit/s: `MP3 320`. A variable bitrate shows the average with a tilde: `MP3 ~245`, and for an album it is the average of the tracks, by length. An album with tracks in different formats shows `mixed`. The status bar shows the format of the track that plays in the same way.

`v` switches the sorting. With **metadata** (the default), the columns are album artists, albums, and tracks. With **folders**, the columns are the top folders, the album folders in them, and the files. The status bar shows the mode and the format of the track that plays, for example `local · metadata · FLAC 44.1/16`.

The local player is mpv, which scopolamine controls through its IPC socket. mpv runs without your mpv configuration and without scripts. It plays gapless and applies the album ReplayGain (the track ReplayGain when the album has none). The volume is the same for both modes.

In the local library, `s` and `D` do nothing. Chrome and MusicKit start only when the Apple Music view shows for the first time.

### Album covers

In kitty and Ghostty, the track column shows the cover of the selected album at the bottom right. When "All albums" is selected, there is no cover. scopolamine uses the kitty graphics protocol with Unicode placeholders. It gets each cover one time and keeps a copy in `~/.cache/scopolamine/art/`.

The cover uses at most half of the column height. When the window is too small, scopolamine shows no cover. In tmux and screen, scopolamine shows no covers. `SCOPOLAMINE_COVERS=0` turns the covers off. `SCOPOLAMINE_COVERS=1` turns them on in a different terminal that supports the protocol.

### Themes

Push `T` to open the theme picker at the top right of the screen. When you move through the list with `↑` / `↓`, the whole screen shows the highlighted theme. `enter` keeps the theme and saves it as `theme` in `~/.config/scopolamine/config.json`. `esc` goes back to the theme that you had.

The first theme, `scopolamine`, uses the scopolamine colors on the background of your terminal. The other themes set the terminal background while scopolamine runs: catppuccin-mocha, catppuccin-latte, catppuccin-frappe, catppuccin-macchiato, nord, dracula, gruvbox-dark, gruvbox-light, tokyo-night, tokyo-night-day, rose-pine, rose-pine-moon, rose-pine-dawn, one-dark, magenta-geode, coral-sunset, lavender-fields-forever, vt100, and vt52. The playing row, the progress bar, and the focused titles use the accent color of the theme.

To use a theme for one run only, start scopolamine with `--theme NAME`. The theme palettes come from [tideui](https://github.com/allisonhere/tideui) by Allie Bayless (MIT license).

### Unavailable tracks

Some albums in a library are not available in the country of the account. Usually the label did not license the album for that country, or the album is no longer in the catalog. Apple does not supply a stream for these tracks. scopolamine shows them as "unavailable" and skips them. The Apple Music apps show them in gray for the same reason.

### Resume

When you quit, scopolamine saves, for each mode, the selected artist, album, and track, the column, the sorting, and the playback position. It also saves the mode that shows. At the next start, it shows that mode with the same items. It does not start the playback. When nothing plays, the status bar shows the last track of the mode with "space resumes".

## Audio quality

On Linux, Apple Music streams are only available through MusicKit JS in a browser with the Widevine DRM module. That path gives a maximum of 256 kbps AAC. scopolamine always asks for the highest bitrate. Chrome decrypts and decodes the stream and sends it to PipeWire or PulseAudio. Apple supplies lossless and Hi-Res audio only to its own apps.

scopolamine puts the full album into one MusicKit queue. MusicKit then goes to the next track on the same media element. vibez measured this method at 20–40 ms between tracks, and a new queue for each track at 450–1000 ms. These measurements are for queues of catalog and library tracks. The queue of library tracks that scopolamine uses is not measured.

## Files

| Path | Contents |
|---|---|
| `~/.config/scopolamine/config.json` | The user token, the volume, the theme, the local library folder (`local_root`), and other settings. Mode 0600. |
| `~/.cache/scopolamine/library.db` | The library cache (SQLite). |
| `~/.cache/scopolamine/session.json` | The resume data of both modes. |
| `~/.cache/scopolamine/webplayer-token.json` | The web player token. |
| `~/.cache/scopolamine/player.log` | The player log of the last run. |
| `~/.cache/scopolamine/art/` | The album covers. |
| `~/.cache/scopolamine/chrome/` | The private copy of Google Chrome. |

`SCOPOLAMINE_CHROME_PATH` or `CHROME_PATH` selects a different Chrome or Chromium. It must have the Widevine module.

## Source layout

```
cmd/scopolamine      command line and start-up
internal/config      config file and directories
internal/devtoken    developer token sources
internal/auth        sign-in page for your own developer token
internal/applemusic  Apple Music API client: library, catalog, add, remove
internal/library     SQLite library cache
internal/localscan   local folder scan: ffprobe, cue sheets, folder watch
internal/player      player interface; cdp: Apple Music in headless Chrome;
                     mpv: local files; router: one player for both
internal/mpris       MPRIS2 D-Bus server
internal/cover       album covers: kitty graphics protocol
internal/tui         terminal user interface
```

## Versions

scopolamine uses [Semantic Versioning](https://semver.org). Before version 1.0.0, a minor version (0.2.0) can change keys, commands, and file formats. [CHANGELOG.md](CHANGELOG.md) lists the changes of each version. `scopolamine --version` and the help screen (`?`) show the version.

## License

[MIT](LICENSE).

Parts of the code come from [vibez](https://github.com/simonepelosi/vibez) by Simone Pelosi, under the MIT license: the sign-in, the Chrome and Widevine playback bridge, the Chrome download, and the MPRIS server. [NOTICE](NOTICE) lists these parts, and [third_party/vibez/LICENSE](third_party/vibez/LICENSE) has the license of vibez.

The theme palettes come from [tideui](https://github.com/allisonhere/tideui) by Allie Bayless, under the MIT license. [third_party/tideui/LICENSE](third_party/tideui/LICENSE) has the license of tideui.

The binary contains third-party Go modules under the MIT, BSD, and Apache 2.0 licenses. [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) has their license texts. `scripts/third-party-licenses.sh` makes that file again after a dependency change.

scopolamine downloads Google Chrome and the Playwright driver at run time. They are not part of this project, and their own licenses apply.

## Disclaimer

scopolamine is not affiliated with Apple or endorsed by Apple. Apple Music and MusicKit are trademarks of Apple Inc.

scopolamine comes "as is", without a warranty of any kind (see the [MIT license](LICENSE)). The author is not liable for any loss of data or other damage that scopolamine causes, directly or indirectly. This includes the albums in your Apple Music library. Use of the web player token can be against the terms of Apple Music. You are responsible for your use of the program.
