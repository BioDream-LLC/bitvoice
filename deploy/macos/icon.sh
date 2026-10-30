#!/bin/sh
# Makes deploy/macos/BitVoice.icns from BitVoice.svg (on a Mac: qlmanage, sips, iconutil).
# The .icns is committed, so a release built anywhere carries it.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
t=$(mktemp -d)
qlmanage -t -s 1024 -o "$t" "$here/BitVoice.svg" >/dev/null
mkdir "$t/BitVoice.iconset"
for s in 16 32 128 256 512; do
  sips -z $s $s "$t/BitVoice.svg.png" --out "$t/BitVoice.iconset/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d "$t/BitVoice.svg.png" --out "$t/BitVoice.iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns -o "$here/BitVoice.icns" "$t/BitVoice.iconset"
rm -rf "$t"
