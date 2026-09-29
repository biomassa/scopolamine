# scopolamine

scopolamine is a terminal program for [Apple Music](https://music.apple.com) on Linux, in Go. It shows your library as three columns: artists, albums, and tracks. It plays full albums. It has no radio, no recommendations, and no playlists.

> This software was developed with the assistance of a LLM.

![scopolamine: the artist, album, and track columns, with an album that plays](docs/screenshot.png)

## Features

- Three columns: artists, albums, and tracks. Each column starts with an "All" row. "All albums" shows all the tracks of an artist, grouped by album.
- Albums in date order, and track numbers on all tracks.
- Full albums play as one queue, so that the tracks follow each other without a new start for each track.
- Album covers at the bottom of the track column, in kitty and Ghostty.
- A filter for each column (`/`).
- A search of the Apple Music catalog (`s`), with the discography of each artist. A mark shows the albums that are in your library. You can play an album from the search, add it to your library (`a`), or remove it (`D`).
- Tracks that are not available in your country show as "unavailable". Playback skips them.
- Resume: at the next start, scopolamine shows the artist, album, and track of the last session. `space` continues the last track at the same position.
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
| `enter` | On an artist: go to the albums. On an album: play it and go to the tracks. On a track: play from this track. |
| `space` | Play or pause. After a restart: continue the last track. |
| `←` `→` | Seek 10 seconds back or forward. `shift` seeks 60 seconds. `,` and `.` also work. |
| `n` `p` | Play the next or the previous track. |
| `+` `-` | Change the volume. |
| `x` | Stop. |
| `o` | Go to the album that plays. |
| `s` | Search Apple Music. |
| `D` | Remove the album from your library. scopolamine asks first. |
| `R` | Sync the album list again. |
| `?` | Show the help and the version. |
| `q` | Quit. |

### "All" rows

"All artists" shows all the albums. "All albums" shows all the tracks of the selected artist, grouped by album. `enter` on "All albums" or on "All" plays all these tracks. When "All artists" is selected, "All albums" shows no tracks, because that needs a request for each album in the library.

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

### Album covers

In kitty and Ghostty, the track column shows the cover of the selected album at the bottom right. When "All albums" is selected, there is no cover. scopolamine uses the kitty graphics protocol with Unicode placeholders. It downloads each cover one time and keeps a copy in `~/.cache/scopolamine/art/`.

The cover uses at most half of the column height. When the window is too small, scopolamine shows no cover. In tmux and screen, scopolamine shows no covers. `SCOPOLAMINE_COVERS=0` turns the covers off. `SCOPOLAMINE_COVERS=1` turns them on in a different terminal that supports the protocol.

### Unavailable tracks

Some albums in a library are not available in the country of the account. Usually the label did not license the album for that country, or the album is no longer in the catalog. Apple does not supply a stream for these tracks. scopolamine shows them as "unavailable" and skips them. The Apple Music apps show them in gray for the same reason.

### Resume

When you quit, scopolamine saves the selected artist, album, and track, the column, and the playback position. At the next start, it selects the same items. It does not start the playback. The status bar shows the last track with "space resumes".

## Audio quality

On Linux, Apple Music streams are only available through MusicKit JS in a browser with the Widevine DRM module. That path gives a maximum of 256 kbps AAC. scopolamine always asks for the highest bitrate. Chrome decrypts and decodes the stream and sends it to PipeWire or PulseAudio. Apple supplies lossless and Hi-Res audio only to its own apps.

scopolamine puts the full album into one MusicKit queue. MusicKit then goes to the next track on the same media element. vibez measured this method at 20–40 ms between tracks, and a new queue for each track at 450–1000 ms. These measurements are for queues of catalog and library tracks. The queue of library tracks that scopolamine uses is not measured.

## Files

| Path | Contents |
|---|---|
| `~/.config/scopolamine/config.json` | The user token, the volume, and other settings. Mode 0600. |
| `~/.cache/scopolamine/library.db` | The library cache (SQLite). |
| `~/.cache/scopolamine/session.json` | The resume data. |
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
internal/player      player interface; cdp: Apple Music in headless Chrome
internal/mpris       MPRIS2 D-Bus server
internal/cover       album covers: kitty graphics protocol
internal/tui         terminal user interface
```

The library cache has a source column for each album. A later version can add local files and a libmpv player behind the same player interface.

## Versions

scopolamine uses [Semantic Versioning](https://semver.org). Before version 1.0.0, a minor version (0.2.0) can change keys, commands, and file formats. [CHANGELOG.md](CHANGELOG.md) lists the changes of each version. `scopolamine --version` and the help screen (`?`) show the version.

## License

[MIT](LICENSE).

Parts of the code come from [vibez](https://github.com/simonepelosi/vibez) by Simone Pelosi, under the MIT license: the sign-in, the Chrome and Widevine playback bridge, the Chrome download, and the MPRIS server. [NOTICE](NOTICE) lists these parts, and [third_party/vibez/LICENSE](third_party/vibez/LICENSE) has the license of vibez.

The binary contains third-party Go modules under the MIT, BSD, and Apache 2.0 licenses. [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md) has their license texts. `scripts/third-party-licenses.sh` makes that file again after a dependency change.

scopolamine downloads Google Chrome and the Playwright driver at run time. They are not part of this project, and their own licenses apply.

## Disclaimer

scopolamine is not affiliated with Apple or endorsed by Apple. Apple Music and MusicKit are trademarks of Apple Inc.

scopolamine comes "as is", without a warranty of any kind (see the [MIT license](LICENSE)). The author is not liable for any loss of data or other damage that scopolamine causes, directly or indirectly. This includes the albums in your Apple Music library. Use of the web player token can be against the terms of Apple Music. You are responsible for your use of the program.
