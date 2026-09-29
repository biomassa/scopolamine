#!/bin/sh
# Writes THIRD_PARTY_LICENSES.md: the license texts of all Go modules that
# are compiled into the scopolamine binary. Run it after a dependency change.
set -eu
cd "$(dirname "$0")/.."
out=THIRD_PARTY_LICENSES.md
{
	echo "# Third-party licenses"
	echo
	echo "The scopolamine binary contains the Go modules below. Each section gives the"
	echo "module, its version, and its license text."
	echo
	echo "This file does not cover the vibez code in this repository. See NOTICE and"
	echo "third_party/vibez/LICENSE for vibez."
	echo
	echo "The program downloads Google Chrome and the Playwright driver at run time."
	echo "They are not part of this repository or the binary. Their own licenses apply."
	go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}' ./cmd/scopolamine |
		sort -u |
		while read -r path version; do
			dir=$(go list -m -f '{{.Dir}}' "$path")
			echo
			echo "## $path $version"
			for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/PATENTS "$dir"/NOTICE*; do
				[ -f "$f" ] || continue
				echo
				echo "$(basename "$f"):"
				echo
				echo '````````'
				cat "$f"
				# Some files lack a final newline; the fence must start a line.
				[ -z "$(tail -c 1 "$f")" ] || echo
				echo '````````'
			done
		done
} >"$out"
echo "wrote $out"
