#!/bin/sh
# Installs BitVoice in one command, on Linux (as a system service) or macOS (for the signed-in user):
#
#   curl -fsSL https://raw.githubusercontent.com/BioDream-LLC/bitvoice/main/deploy/install.sh | sh
#
# It downloads the latest release for this computer from GitHub, checks it against the release's
# SHA256SUMS and BioDream's release signature (ed25519, with OpenSSL 3), and installs it:
#   Linux: /usr/local/bin/bitvoice, a "bitvoice" system user, data in /var/lib/bitvoice, the systemd
#          service bitvoice (web interface on port 443), and the firewall opened when ufw or firewalld runs.
#   macOS: ~/Applications/BitVoice/bitvoice and a launchd agent (web interface on port 8443, data in
#          ~/Library/Application Support/BitVoice); it runs while you are signed in.
# Run it again to upgrade. Options (after "sh -s --" when piped):
#   --version vX.Y.Z   that release instead of the latest
#   --uninstall        remove the program and the service; the data stays (--purge removes it too)
#   --no-service       install the program only
#   --no-firewall      leave the firewall alone
# For a copy kept off the internet: BITVOICE_FROM=/dir/with/the/archive/and/SHA256SUMS.
set -eu

REPO=BioDream-LLC/bitvoice
# BioDream's release public key (ed25519, base64), the same one release builds carry.
# BITVOICE_RELEASE_KEY replaces it, to install your own signed builds (make release).
RELEASE_KEY=${BITVOICE_RELEASE_KEY:-M6UaGaLM1T0SekufRzAlUSeSTjfxpVL18YHE5eKkKxY=}
KEY_NAME="BioDream's release key"
[ "$RELEASE_KEY" = M6UaGaLM1T0SekufRzAlUSeSTjfxpVL18YHE5eKkKxY= ] || KEY_NAME="the release key in BITVOICE_RELEASE_KEY"
VERSION=${BITVOICE_VERSION:-}
FROM=${BITVOICE_FROM:-}
HTTPS_LINUX=${BITVOICE_HTTPS:-:443}
HTTPS_MAC=${BITVOICE_HTTPS:-:8443}
DATA_MAC=${BITVOICE_DATA:-} # macOS: another data folder than ~/Library/Application Support/BitVoice
ACTION=install
SERVICE=1
FIREWALL=1
PURGE=0

say() { printf '%s\n' "$*"; }
fetch() { # url file
  if command -v curl >/dev/null 2>&1; then curl -fsSL --proto '=https' --tlsv1.2 "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q --https-only -O "$2" "$1"
  else fail "needs curl or wget"; fi
}
fail() { printf 'BitVoice install: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || fail "--version needs a version, like v0.1.5"; VERSION=$2; shift ;;
    --version=*) VERSION=${1#--version=} ;;
    --uninstall) ACTION=uninstall ;;
    --purge) ACTION=uninstall; PURGE=1 ;;
    --no-service) SERVICE=0 ;;
    --no-firewall) FIREWALL=0 ;;
    -h|--help) sed -n '2,19p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) fail "unknown option $1 (try --help)" ;;
  esac
  shift
done
case "$VERSION" in ''|v[0-9]*.[0-9]*.[0-9]*) ;; *) fail "a version looks like v0.1.5, not $VERSION" ;; esac

OS=$(uname -s)
case "$OS" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "this script is for Linux and macOS; on Windows use deploy/install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "BitVoice is built for x86-64 and ARM64, not $(uname -m)" ;;
esac

# Linux installs a system service: it needs root. Run again through sudo when it is there.
if [ "$os" = linux ] && [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then
    say "Installing a system service needs root: asking sudo."
    tmp_self=$(mktemp)
    chmod 0644 "$tmp_self"
    if [ -f "$0" ] && [ "$(basename "$0")" != sh ] && [ "$(basename "$0")" != dash ] && [ "$(basename "$0")" != bash ]; then
      cp "$0" "$tmp_self"
    else
      fetch "https://raw.githubusercontent.com/$REPO/main/deploy/install.sh" "$tmp_self" || fail "could not fetch the installer again for sudo"
    fi
    set -- 
    [ "$ACTION" = uninstall ] && set -- "$@" --uninstall
    [ "$PURGE" = 1 ] && set -- "$@" --purge
    [ "$SERVICE" = 0 ] && set -- "$@" --no-service
    [ "$FIREWALL" = 0 ] && set -- "$@" --no-firewall
    [ -n "$VERSION" ] && set -- "$@" --version "$VERSION"
    exec sudo BITVOICE_FROM="$FROM" BITVOICE_RELEASE_KEY="$RELEASE_KEY" BITVOICE_HTTPS="${BITVOICE_HTTPS:-}" \
      BITVOICE_REQUIRE_SIGNATURE="${BITVOICE_REQUIRE_SIGNATURE:-}" sh "$tmp_self" "$@"
  fi
  fail "run this as root (or install sudo)"
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  else openssl dgst -sha256 -r "$1" | cut -d' ' -f1; fi
}

# The program's signature: base64 of an ed25519 signature over the file. OpenSSL 3 checks it with the
# key as a PEM public key (the 12-byte ed25519 SubjectPublicKeyInfo prefix is MCowBQYDK2VwAyEA in base64).
verify_signature() { # program sigfile
  if ! command -v openssl >/dev/null 2>&1 || ! openssl version 2>/dev/null | grep -Eq '^OpenSSL ([3-9]|[1-9][0-9])\.'; then
    [ "${BITVOICE_REQUIRE_SIGNATURE:-}" = 1 ] && fail "OpenSSL 3 is needed to check the release signature"
    say "  (OpenSSL 3 is not here, so the signature is not checked; the SHA-256 sum was.)"
    return 0
  fi
  [ -f "$2" ] || fail "$(basename "$1") has no signature beside it: this is not a BioDream release"
  printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$RELEASE_KEY" > "$work/key.pem"
  openssl base64 -d -A -in "$2" -out "$work/sig.bin" 2>/dev/null || fail "$(basename "$2") is damaged"
  if openssl pkeyutl -verify -pubin -inkey "$work/key.pem" -rawin -in "$1" -sigfile "$work/sig.bin" >/dev/null 2>&1; then
    say "  Signature checked: signed by $KEY_NAME."
  else
    fail "the signature of $(basename "$1") does not match $KEY_NAME: not installed"
  fi
}

# --- uninstall ---------------------------------------------------------------------------------
if [ "$ACTION" = uninstall ]; then
  if [ "$os" = linux ]; then
    if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/bitvoice.service ]; then
      systemctl disable --now bitvoice >/dev/null 2>&1 || true
      rm -f /etc/systemd/system/bitvoice.service
      systemctl daemon-reload
    fi
    rm -f /usr/local/bin/bitvoice /usr/local/bin/bitvoice-dahdi
    if [ "$PURGE" = 1 ]; then
      rm -rf /var/lib/bitvoice
      userdel bitvoice >/dev/null 2>&1 || true
      say "BitVoice and its data are removed."
    else
      say "BitVoice is removed. Its data stays in /var/lib/bitvoice (--purge removes it)."
    fi
  else
    plist="$HOME/Library/LaunchAgents/org.bitvoice.bitvoice.plist"
    launchctl bootout "gui/$(id -u)" "$plist" >/dev/null 2>&1 || launchctl unload "$plist" >/dev/null 2>&1 || true
    rm -f "$plist"
    rm -rf "$HOME/Applications/BitVoice"
    if [ "$PURGE" = 1 ]; then
      rm -rf "$HOME/Library/Application Support/BitVoice"
      say "BitVoice and its data are removed."
    else
      say "BitVoice is removed. Its data stays in ~/Library/Application Support/BitVoice (--purge removes it)."
    fi
  fi
  exit 0
fi

# --- download and check ------------------------------------------------------------------------
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

if [ -z "$VERSION" ]; then
  if [ -n "$FROM" ]; then
    VERSION=$(ls "$FROM" | sed -n "s/^bitvoice-\(v[0-9][0-9.]*\)-$os-$arch\.tar\.gz$/\1/p" | sort -t. -k1,1 -k2,2n -k3,3n | tail -n 1)
  else
    fetch "https://api.github.com/repos/$REPO/releases/latest" "$work/latest.json" || fail "could not ask GitHub for the latest release"
    VERSION=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(v[0-9][0-9.]*\)".*/\1/p' "$work/latest.json" | head -n 1)
  fi
  [ -n "$VERSION" ] || fail "could not find the latest release"
fi
name="bitvoice-$VERSION-$os-$arch"
say "Installing BitVoice $VERSION for $os/$arch."
if [ -n "$FROM" ]; then
  cp "$FROM/$name.tar.gz" "$FROM/SHA256SUMS" "$work/" || fail "$FROM has no $name.tar.gz and SHA256SUMS"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
  fetch "$base/$name.tar.gz" "$work/$name.tar.gz" || fail "could not download $name.tar.gz"
  fetch "$base/SHA256SUMS" "$work/SHA256SUMS" || fail "could not download SHA256SUMS"
fi
want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$work/SHA256SUMS")
[ -n "$want" ] || fail "SHA256SUMS does not list $name.tar.gz"
[ "$(sha256 "$work/$name.tar.gz")" = "$want" ] || fail "$name.tar.gz does not match its SHA-256 sum: the download is damaged"
say "  SHA-256 checked."
tar -xzf "$work/$name.tar.gz" -C "$work"
[ -x "$work/$name/bitvoice" ] || fail "the archive holds no bitvoice program"
verify_signature "$work/$name/bitvoice" "$work/$name/bitvoice.sig"
if [ -f "$work/$name/bitvoice-dahdi" ]; then
  verify_signature "$work/$name/bitvoice-dahdi" "$work/$name/bitvoice-dahdi.sig"
fi

wait_ready() { # url
  i=0
  while [ $i -lt 60 ]; do
    if command -v curl >/dev/null 2>&1; then
      curl -fsk "$1/api/health/live" >/dev/null 2>&1 && return 0
    else
      wget -q --no-check-certificate -O /dev/null "$1/api/health/live" 2>/dev/null && return 0
    fi
    i=$((i + 1)); sleep 1
  done
  return 1
}

port_of() { p=${1##*:}; [ -n "$p" ] && printf '%s' "$p" || printf 443; }

# --- Linux: a system service -------------------------------------------------------------------
if [ "$os" = linux ]; then
  upgrade=0
  [ -f /usr/local/bin/bitvoice ] && upgrade=1
  install -m 0755 "$work/$name/bitvoice" /usr/local/bin/bitvoice.new && mv -f /usr/local/bin/bitvoice.new /usr/local/bin/bitvoice
  [ -f "$work/$name/bitvoice-dahdi" ] && install -m 0755 "$work/$name/bitvoice-dahdi" /usr/local/bin/bitvoice-dahdi
  say "  Program: /usr/local/bin/bitvoice"
  if [ "$SERVICE" = 0 ]; then
    say "Installed. Start it with: bitvoice -data /var/lib/bitvoice"
    exit 0
  fi
  command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ] || fail "this system does not run systemd: use --no-service and start bitvoice yourself"
  if ! id bitvoice >/dev/null 2>&1; then
    nologin=/usr/sbin/nologin; [ -x "$nologin" ] || nologin=/sbin/nologin; [ -x "$nologin" ] || nologin=/bin/false
    useradd --system --home-dir /var/lib/bitvoice --shell "$nologin" bitvoice 2>/dev/null ||
      adduser -S -h /var/lib/bitvoice -s "$nologin" bitvoice # Alpine's busybox
  fi
  mkdir -p /var/lib/bitvoice
  chown bitvoice:bitvoice /var/lib/bitvoice
  chmod 0750 /var/lib/bitvoice
  # The same unit as deploy/cloud/bitvoice.service (a test keeps them together), with the address chosen.
  cat > /etc/systemd/system/bitvoice.service <<UNIT
# systemd unit for BitVoice on a server or cloud image. The installed program is the launcher for
# updates, so this unit never needs changing when BitVoice is upgraded from its own screen.
[Unit]
Description=BitVoice phone system
After=network-online.target
Wants=network-online.target

[Service]
User=bitvoice
Group=bitvoice
ExecStart=/usr/local/bin/bitvoice -data /var/lib/bitvoice -no-browser -https $HTTPS_LINUX
AmbientCapabilities=CAP_NET_BIND_SERVICE
Restart=on-failure
RestartSec=2
LimitNOFILE=65536
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/bitvoice
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  if [ "$upgrade" = 1 ] && systemctl is-enabled bitvoice >/dev/null 2>&1; then
    systemctl restart bitvoice
  else
    systemctl enable --now bitvoice >/dev/null 2>&1
  fi
  say "  Service: bitvoice (systemctl status bitvoice; logs: journalctl -u bitvoice)"
  port=$(port_of "$HTTPS_LINUX")
  if [ "$FIREWALL" = 1 ]; then
    if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
      for r in "$port/tcp" 5060/udp 5060/tcp 5061/tcp 10000:20000/udp; do ufw allow "$r" >/dev/null; done
      say "  Firewall (ufw): opened $port/tcp (web), 5060 (SIP), 5061 (SIP over TLS), 10000-20000/udp (calls' audio)."
    elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
      for r in "$port/tcp" 5060/udp 5060/tcp 5061/tcp 10000-20000/udp; do firewall-cmd -q --permanent --add-port="$r"; done
      firewall-cmd -q --reload
      say "  Firewall (firewalld): opened $port/tcp (web), 5060 (SIP), 5061 (SIP over TLS), 10000-20000/udp (calls' audio)."
    fi
  fi
  url="https://127.0.0.1:$port"
  [ "$port" = 443 ] && url="https://127.0.0.1"
  if ! wait_ready "$url"; then
    journalctl -u bitvoice -n 30 --no-pager 2>/dev/null || true
    fail "BitVoice did not start: see the log above"
  fi
  addr=$(hostname -I 2>/dev/null | awk '{print $1}')
  [ -n "$addr" ] || addr=127.0.0.1
  [ "$port" = 443 ] && open_url="https://$addr" || open_url="https://$addr:$port"
  say ""
  say "BitVoice $VERSION is running. Open $open_url in a browser to set it up."
  code=$(journalctl -u bitvoice --no-pager 2>/dev/null | sed -n 's/.*Setup code for another computer: \([^ ]*\).*/\1/p' | tail -n 1)
  [ -n "$code" ] && say "  Setup code (the wizard asks for it from another computer): $code"
  say "  The browser warns once about BitVoice's own certificate until you give it yours (Settings, Certificate)."
  exit 0
fi

# --- macOS: for the signed-in user -------------------------------------------------------------
dir="$HOME/Applications/BitVoice"
mkdir -p "$dir"
cp "$work/$name/bitvoice" "$dir/bitvoice.new" && chmod 0755 "$dir/bitvoice.new" && mv -f "$dir/bitvoice.new" "$dir/bitvoice"
cp "$work/$name/bitvoice.sig" "$work/$name/LICENSE" "$work/$name/NOTICE" "$dir/" 2>/dev/null || true
[ -f "$work/$name/bitvoice-manual.pdf" ] && cp "$work/$name/bitvoice-manual.pdf" "$dir/"
xattr -d com.apple.quarantine "$dir/bitvoice" 2>/dev/null || true
say "  Program: $dir/bitvoice"
if [ "$SERVICE" = 0 ]; then
  say "Installed. Start it with: \"$dir/bitvoice\""
  exit 0
fi
plist="$HOME/Library/LaunchAgents/org.bitvoice.bitvoice.plist"
mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
launchctl bootout "gui/$(id -u)" "$plist" >/dev/null 2>&1 || true
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>org.bitvoice.bitvoice</string>
  <key>ProgramArguments</key>
  <array>
    <string>$dir/bitvoice</string>$( [ -n "$DATA_MAC" ] && printf '\n    <string>-data</string>\n    <string>%s</string>' "$DATA_MAC")
    <string>-no-browser</string>
    <string>-https</string>
    <string>$HTTPS_MAC</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>$HOME/Library/Logs/BitVoice.log</string>
  <key>StandardErrorPath</key><string>$HOME/Library/Logs/BitVoice.log</string>
</dict>
</plist>
PLIST
launchctl bootstrap "gui/$(id -u)" "$plist" 2>/dev/null || launchctl load "$plist"
say "  Starts when you sign in (launchd agent org.bitvoice.bitvoice; log: ~/Library/Logs/BitVoice.log)."
port=$(port_of "$HTTPS_MAC")
if ! wait_ready "https://127.0.0.1:$port"; then
  tail -n 30 "$HOME/Library/Logs/BitVoice.log" 2>/dev/null || true
  fail "BitVoice did not start: see the log above"
fi
say ""
say "BitVoice $VERSION is running: open https://127.0.0.1:$port"
say "  The browser warns once about BitVoice's own certificate until you give it yours (Settings, Certificate)."
say "  macOS asks once whether BitVoice may accept incoming connections: allow it, so phones can connect."
