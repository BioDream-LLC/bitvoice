#!/bin/sh
# Builds whisper.cpp's whisper-server for BitVoice's downloads: one program with nothing to install beside
# it (static libraries, the C++ runtime linked in where the platform allows), from a pinned, checked
# source archive. BitVoice runs it for speech to text on this server (Settings, AI).
#
#   deploy/whisper/build.sh OUT_DIR TARGET
#
# TARGET is the download it is for: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64 or
# windows-arm64, built on that system (darwin-amd64 also on an Apple silicon Mac). Linux programs are best
# built on an old glibc (Debian 11) so they run on every current distribution. On x86-64 the program
# needs AVX2 and FMA (every server processor since about 2013); on Arm 64 it uses NEON; on Apple silicon
# the GPU (Metal).
set -eu
VERSION=1.9.5
SHA256=ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720
OUT=${1:?usage: build.sh OUT_DIR TARGET}
TARGET=${2:?usage: build.sh OUT_DIR TARGET}
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
url=https://github.com/ggml-org/whisper.cpp/archive/refs/tags/v$VERSION.tar.gz
if [ -n "${WHISPER_SOURCE:-}" ]; then cp "$WHISPER_SOURCE" src.tar.gz; else curl -sSfL -o src.tar.gz "$url"; fi
got=$( (sha256sum src.tar.gz 2>/dev/null || shasum -a 256 src.tar.gz) | cut -d' ' -f1)
[ "$got" = "$SHA256" ] || { echo "whisper.cpp source archive is not the pinned one ($got)"; exit 1; }
tar xzf src.tar.gz
cd whisper.cpp-$VERSION

set -- -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=OFF -DWHISPER_BUILD_TESTS=OFF \
  -DWHISPER_BUILD_EXAMPLES=ON -DWHISPER_SDL2=OFF -DWHISPER_CURL=OFF -DGGML_OPENMP=OFF -DGGML_CCACHE=OFF
x86="-DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON"
case "$TARGET" in
  darwin-arm64) set -- "$@" -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON -DCMAKE_OSX_ARCHITECTURES=arm64 -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0 ;;
  darwin-amd64) set -- "$@" -DGGML_METAL=OFF -DGGML_BLAS=OFF -DCMAKE_OSX_ARCHITECTURES=x86_64 -DCMAKE_OSX_DEPLOYMENT_TARGET=12.0 $x86 ;;
  linux-amd64) set -- "$@" "-DCMAKE_EXE_LINKER_FLAGS=-static-libstdc++ -static-libgcc" $x86 ;;
  linux-arm64) set -- "$@" "-DCMAKE_EXE_LINKER_FLAGS=-static-libstdc++ -static-libgcc" ;;
  windows-amd64) set -- "$@" -A x64 -DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded $x86 ;;
  windows-arm64) set -- "$@" -A ARM64 -T ClangCL -DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded ;;
  *) echo "unknown target $TARGET"; exit 1 ;;
esac
cmake -B build "$@" > "$OUT/build.log" 2>&1 || { tail -40 "$OUT/build.log"; exit 1; }
cmake --build build --config Release --target whisper-server -j 4 >> "$OUT/build.log" 2>&1 || { tail -40 "$OUT/build.log"; exit 1; }
exe=$(find build -type f \( -name whisper-server -o -name whisper-server.exe \) | head -1)
[ -n "$exe" ] || { echo "no whisper-server was built"; exit 1; }
cp "$exe" "$OUT/"
cp LICENSE "$OUT/whisper.cpp-LICENSE"
rm -f "$OUT/build.log"
echo "built $OUT/$(basename "$exe") (whisper.cpp $VERSION)"
