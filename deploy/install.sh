#!/bin/sh
# Installs BitVoice in one command, on Linux (as a system service) or macOS (for the signed-in user):
#
#   curl -fsSL https://raw.githubusercontent.com/BioDream-LLC/bitvoice/main/deploy/install.sh | sh
#
# It downloads the latest release for this computer from GitHub, checks it against the release's
# SHA256SUMS and BioDream's release signature (ed25519, with OpenSSL 3 or OpenSSH's ssh-keygen), and installs it:
#   Linux: /usr/local/bin/bitvoice, a "bitvoice" system user, data in /var/lib/bitvoice, the systemd
#          service bitvoice (web interface on port 443), and the firewall opened when ufw or firewalld runs.
#   macOS: BitVoice.app in ~/Applications (Launchpad and Spotlight open its web interface) and a launchd
#          agent: web interface on port 8443, data in ~/Library/Application Support/BitVoice, logs in
#          ~/Library/Logs/BitVoice; it runs while you are signed in. With sudo, for a Mac that answers calls
#          with nobody signed in: /Applications/BitVoice.app and a launchd daemon that starts with the Mac,
#          as the hidden user _bitvoice, web interface on port 443, data in /Library/Application Support/BitVoice.
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
  # Never wait forever: give up on a server that does not answer in 30 s or sends under 1 KB/s for a minute.
  if command -v curl >/dev/null 2>&1; then curl -fsSL --proto '=https' --tlsv1.2 --connect-timeout 30 -y 60 -Y 1024 "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then wget -q --https-only -T 60 -t 2 -O "$2" "$1"
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
    -h|--help) sed -n '2,20p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0 ;;
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

# macOS: the signed-in user's BitVoice, or with sudo (root) the Mac's own, which starts with the Mac.
MAC_SYSTEM=0
[ "$os" = darwin ] && [ "$(id -u)" -eq 0 ] && MAC_SYSTEM=1
mac_paths() {
  if [ "$MAC_SYSTEM" = 1 ]; then
    app=/Applications/BitVoice.app
    data=${DATA_MAC:-"/Library/Application Support/BitVoice"}
    logs=/Library/Logs/BitVoice
    plist=/Library/LaunchDaemons/org.bitvoice.bitvoice.plist
    https=${BITVOICE_HTTPS:-:443}
  else
    app="$HOME/Applications/BitVoice.app"
    data=${DATA_MAC:-"$HOME/Library/Application Support/BitVoice"}
    logs="$HOME/Library/Logs/BitVoice"
    plist="$HOME/Library/LaunchAgents/org.bitvoice.bitvoice.plist"
    https=$HTTPS_MAC
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1
  else openssl dgst -sha256 -r "$1" | cut -d' ' -f1; fi
}

# The program's signature, with BioDream's ed25519 key: with OpenSSL 3 when it is here (the .sig beside
# the program: base64 of the signature, the key as a PEM public key whose 12-byte prefix is
# MCowBQYDK2VwAyEA in base64), or else with OpenSSH's ssh-keygen (8.1 and later: macOS, Ubuntu 20.04,
# which have no OpenSSL 3) and the same key's OpenSSH signature (.sshsig, from v0.1.14 on).
openssl3() { command -v openssl >/dev/null 2>&1 && openssl version 2>/dev/null | grep -Eq '^OpenSSL ([3-9]|[1-9][0-9])\.'; }
sshsig_ok() { command -v ssh-keygen >/dev/null 2>&1 && ssh-keygen -Y verify 2>&1 | grep -q 'namespace'; }
at_least() { # version minimum: both like v0.1.14
  [ "$(printf '%s\n%s\n' "${1#v}" "${2#v}" | sort -t. -k1,1n -k2,2n -k3,3n | head -n 1)" = "${2#v}" ]
}
unbase64() { base64 -d 2>/dev/null || base64 -D; } # GNU and BSD
verify_signature() { # program (its .sig and .sshsig are beside it)
  if openssl3; then
    [ -f "$1.sig" ] || fail "$(basename "$1") has no signature beside it: this is not a BioDream release"
    printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$RELEASE_KEY" > "$work/key.pem"
    openssl base64 -d -A -in "$1.sig" -out "$work/sig.bin" 2>/dev/null || fail "$(basename "$1").sig is damaged"
    openssl pkeyutl -verify -pubin -inkey "$work/key.pem" -rawin -in "$1" -sigfile "$work/sig.bin" >/dev/null 2>&1 ||
      fail "the signature of $(basename "$1") does not match $KEY_NAME: not installed"
    say "  Signature of $(basename "$1") checked: signed by $KEY_NAME."
  elif sshsig_ok && [ -f "$1.sshsig" ]; then
    # The key as OpenSSH writes it: string "ssh-ed25519", string key (32 bytes).
    blob=$({ printf '\000\000\000\013ssh-ed25519\000\000\000\040'; printf '%s' "$RELEASE_KEY" | unbase64; } | base64 | tr -d '\n')
    printf 'biodream ssh-ed25519 %s\n' "$blob" > "$work/signers"
    ssh-keygen -Y verify -f "$work/signers" -I biodream -n file -s "$1.sshsig" < "$1" >/dev/null 2>&1 ||
      fail "the signature of $(basename "$1") does not match $KEY_NAME: not installed"
    say "  Signature of $(basename "$1") checked (with ssh-keygen): signed by $KEY_NAME."
  elif sshsig_ok && at_least "$VERSION" v0.1.14; then
    fail "$(basename "$1") has no OpenSSH signature (.sshsig) beside it: this is not a BioDream release"
  else
    [ "${BITVOICE_REQUIRE_SIGNATURE:-}" = 1 ] && fail "the release signature cannot be checked here: it needs OpenSSL 3, or ssh-keygen and a release from v0.1.14 on"
    say "  (The signature of $(basename "$1") cannot be checked here, so it is not; the SHA-256 sum was.)"
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
    rm -f /usr/local/bin/bitvoice /usr/local/bin/bitvoice-dahdi /usr/local/bin/whisper-server
    if [ "$PURGE" = 1 ]; then
      rm -rf /var/lib/bitvoice
      userdel bitvoice >/dev/null 2>&1 || true
      say "BitVoice and its data are removed."
    else
      say "BitVoice is removed. Its data stays in /var/lib/bitvoice (--purge removes it)."
    fi
  else
    mac_paths
    if [ "$MAC_SYSTEM" = 1 ]; then
      launchctl bootout system/org.bitvoice.bitvoice >/dev/null 2>&1 || true
      [ -x "$app/Contents/MacOS/bitvoice" ] && /usr/libexec/ApplicationFirewall/socketfilterfw --remove "$app/Contents/MacOS/bitvoice" >/dev/null 2>&1 || true
    else
      launchctl bootout "gui/$(id -u)/org.bitvoice.bitvoice" >/dev/null 2>&1 || true
    fi
    rm -f "$plist"
    rm -rf "$app" "$HOME/Applications/BitVoice" # (and the folder releases before v0.1.14 used)
    if [ "$PURGE" = 1 ]; then
      rm -rf "$data" "$logs"
      [ "$MAC_SYSTEM" = 1 ] || rm -f "$HOME/Library/Logs/BitVoice.log"
      if [ "$MAC_SYSTEM" = 1 ]; then
        dscl . -delete /Users/_bitvoice >/dev/null 2>&1 || true
        dscl . -delete /Groups/_bitvoice >/dev/null 2>&1 || true
      fi
      say "BitVoice and its data are removed."
    else
      say "BitVoice is removed. Its data stays in $data (--purge removes it)."
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
verify_signature "$work/$name/bitvoice"
for extra in bitvoice-dahdi whisper-server; do
  [ -f "$work/$name/$extra" ] && verify_signature "$work/$name/$extra"
done

wait_ready() { # url
  i=0
  while [ $i -lt 60 ]; do
    if command -v curl >/dev/null 2>&1; then
      curl -fsk -m 3 "$1/api/health/live" >/dev/null 2>&1 && return 0
    else
      wget -q -T 3 -t 1 --no-check-certificate -O /dev/null "$1/api/health/live" 2>/dev/null && return 0
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
  # whisper-server: speech to text on this server (Settings, AI), started by BitVoice when chosen.
  [ -f "$work/$name/whisper-server" ] && install -m 0755 "$work/$name/whisper-server" /usr/local/bin/whisper-server
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
  case $addr in
    10.* | 192.168.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].*)
      # A cloud server's own address is private: from the internet, it is reached at its public one.
      say "  That is this machine's address on its own network. From elsewhere (a cloud server), use its public"
      say "  address or name instead, with the port open in the provider's firewall." ;;
  esac
  code=$(journalctl -u bitvoice --no-pager 2>/dev/null | sed -n 's/.*Setup code for another computer: \([^ ]*\).*/\1/p' | tail -n 1)
  [ -n "$code" ] && say "  Setup code (the wizard asks for it from another computer): $code"
  say "  The browser warns once about BitVoice's own certificate until you give it yours (Settings, Certificate)."
  exit 0
fi

# --- macOS ------------------------------------------------------------------------------------
mac_paths
port=$(port_of "$https")
[ "$port" = 443 ] && url="https://127.0.0.1" || url="https://127.0.0.1:$port"
# One BitVoice per Mac from this installer: the user's and the Mac's own would both answer phones.
if [ "$MAC_SYSTEM" = 1 ]; then
  for f in /Users/*/Library/LaunchAgents/org.bitvoice.bitvoice.plist; do
    [ -f "$f" ] && fail "$(echo "$f" | cut -d/ -f3) has BitVoice installed for themselves: remove it first (sh install.sh --uninstall, without sudo)"
  done
elif [ -f /Library/LaunchDaemons/org.bitvoice.bitvoice.plist ]; then
  fail "BitVoice is installed for this whole Mac (with sudo): upgrade or remove it with sudo"
fi
if [ "$MAC_SYSTEM" = 1 ]; then domain=system; else domain="gui/$(id -u)"; fi
# Stop the BitVoice being upgraded, then make sure nothing else holds its web port.
had=0; [ -f "$plist" ] && had=1
launchctl bootout "$domain/org.bitvoice.bitvoice" >/dev/null 2>&1 || true
held() { # anything listening on the port, on any address (lsof -b: never waits on the kernel)
  [ -n "$(lsof -b -nP -iTCP:"$port" -sTCP:LISTEN -t </dev/null 2>/dev/null)" ] || nc -z -G 2 127.0.0.1 "$port" </dev/null >/dev/null 2>&1
}
i=0; while [ $i -lt 20 ] && held; do sleep 0.5; i=$((i + 1)); done
if held; then
  who=$(lsof -b -nP -iTCP:"$port" -sTCP:LISTEN </dev/null 2>/dev/null | awk 'NR == 2 {print $1 " (process " $2 ")"}')
  [ "$had" = 1 ] && launchctl bootstrap "$domain" "$plist" >/dev/null 2>&1
  fail "port $port is in use by ${who:-another program}: stop it, or choose another port with BITVOICE_HTTPS=:8444"
fi

# The app: Launchpad and Spotlight find it, and opening it opens the web interface. The program and
# whisper-server live inside it.
new="$app.new"
rm -rf "$new"
mkdir -p "$new/Contents/MacOS" "$new/Contents/Resources"
cp "$work/$name/bitvoice" "$new/Contents/MacOS/bitvoice" && chmod 0755 "$new/Contents/MacOS/bitvoice"
for f in bitvoice.sig bitvoice.sshsig LICENSE NOTICE bitvoice-manual.pdf; do
  [ -f "$work/$name/$f" ] && cp "$work/$name/$f" "$new/Contents/Resources/"
done
[ -f "$work/$name/BitVoice.icns" ] && cp "$work/$name/BitVoice.icns" "$new/Contents/Resources/"
if [ -f "$work/$name/whisper-server" ]; then
  cp "$work/$name/whisper-server" "$new/Contents/MacOS/whisper-server" && chmod 0755 "$new/Contents/MacOS/whisper-server"
  for f in whisper-server.sig whisper-server.sshsig whisper.cpp-LICENSE; do
    [ -f "$work/$name/$f" ] && cp "$work/$name/$f" "$new/Contents/Resources/"
  done
fi
cat > "$new/Contents/Info.plist" <<INFO
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>org.bitvoice.BitVoice</string>
  <key>CFBundleName</key><string>BitVoice</string>
  <key>CFBundleDisplayName</key><string>BitVoice</string>
  <key>CFBundleExecutable</key><string>launch</string>
  <key>CFBundleIconFile</key><string>BitVoice</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION#v}</string>
  <key>CFBundleVersion</key><string>${VERSION#v}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHumanReadableCopyright</key><string>BioDream LLC. Apache License 2.0.</string>
</dict>
</plist>
INFO
# Opening the app: start BitVoice if it was stopped (the signed-in user's), wait for it, open the page.
# (Named launch: Mac disks ignore case, so "BitVoice" would be the program "bitvoice" itself.)
cat > "$new/Contents/MacOS/launch" <<OPEN
#!/bin/sh
# Opens BitVoice's web interface (written by install.sh).
up() { /usr/bin/curl -fsk -m 2 "$url/api/health/live" >/dev/null 2>&1; }
if ! up && [ "$MAC_SYSTEM" = 0 ] && [ -f "$plist" ]; then
  /bin/launchctl bootstrap "gui/\$(id -u)" "$plist" >/dev/null 2>&1 || /bin/launchctl kickstart "gui/\$(id -u)/org.bitvoice.bitvoice" >/dev/null 2>&1
  i=0; while [ \$i -lt 30 ] && ! up; do sleep 1; i=\$((i + 1)); done
fi
if up; then exec /usr/bin/open "$url"; fi
/usr/bin/osascript -e 'display alert "BitVoice is not running" message "Its log is in $logs." as critical' >/dev/null 2>&1
OPEN
chmod 0755 "$new/Contents/MacOS/launch"
xattr -dr com.apple.quarantine "$new" 2>/dev/null || true
rm -rf "$app" "$HOME/Applications/BitVoice" # (and the folder releases before v0.1.14 used)
mv "$new" "$app"
prog="$app/Contents/MacOS/bitvoice"
# Tell Launchpad and Spotlight about it now (in the background: it may take a while, never the install).
(/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$app" </dev/null >/dev/null 2>&1 &)
say "  Program: $app"
if [ "$SERVICE" = 0 ]; then
  say "Installed. Start it with: \"$prog\""
  exit 0
fi

mkdir -p "$data" "$logs" "$(dirname "$plist")"
if [ "$MAC_SYSTEM" = 1 ]; then
  # A hidden user of its own, as Apple's own services have.
  if ! dscl . -read /Users/_bitvoice >/dev/null 2>&1; then
    id=300
    while dscl . -search /Users UniqueID $id 2>/dev/null | grep -q . || dscl . -search /Groups PrimaryGroupID $id 2>/dev/null | grep -q .; do id=$((id + 1)); done
    dscl . -create /Groups/_bitvoice PrimaryGroupID $id
    dscl . -create /Groups/_bitvoice RealName "BitVoice"
    dscl . -create /Groups/_bitvoice Password '*'
    dscl . -create /Users/_bitvoice UniqueID $id
    dscl . -create /Users/_bitvoice PrimaryGroupID $id
    dscl . -create /Users/_bitvoice RealName "BitVoice"
    dscl . -create /Users/_bitvoice NFSHomeDirectory /var/empty
    dscl . -create /Users/_bitvoice UserShell /usr/bin/false
    dscl . -create /Users/_bitvoice Password '*'
    dscl . -create /Users/_bitvoice IsHidden 1
  fi
  chown -R _bitvoice:_bitvoice "$data" "$logs"
fi
chmod 0700 "$data"
# Its own log, kept under 10 MB (-log, from v0.1.14 on); launchd's file then catches only a failed start.
if [ -n "$VERSION" ] && at_least "$VERSION" v0.1.14; then
  logargs="    <string>-log</string>
    <string>$logs/bitvoice.log</string>"
  out="$logs/launchd.log"
else
  logargs=""
  out="$logs/bitvoice.log"
fi
# The service runs the program kept awake by caffeinate: a sleeping Mac answers no calls.
user=""
[ "$MAC_SYSTEM" = 1 ] && user="  <key>UserName</key><string>_bitvoice</string>
  <key>GroupName</key><string>_bitvoice</string>"
cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>org.bitvoice.bitvoice</string>
  <key>AssociatedBundleIdentifiers</key><string>org.bitvoice.BitVoice</string>
$user
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/caffeinate</string>
    <string>-i</string>
    <string>-s</string>
    <string>$prog</string>
    <string>-data</string>
    <string>$data</string>
$logargs
    <string>-no-browser</string>
    <string>-https</string>
    <string>$https</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>$out</string>
  <key>StandardErrorPath</key><string>$out</string>
</dict>
</plist>
PLIST
[ "$MAC_SYSTEM" = 1 ] && chown root:wheel "$plist" && chmod 0644 "$plist"
launchctl bootstrap "$domain" "$plist" 2>/dev/null || launchctl load "$plist"
if [ "$MAC_SYSTEM" = 1 ]; then
  say "  Starts with the Mac, before anyone signs in (launchd daemon org.bitvoice.bitvoice, as the hidden user _bitvoice)."
else
  say "  Starts when you sign in (launchd agent org.bitvoice.bitvoice). Open BitVoice from Launchpad or Spotlight."
fi
say "  Logs: $logs. Keeps the Mac awake while it runs (caffeinate): a sleeping Mac answers no calls."
# The Mac's firewall, when it is on, asks whether BitVoice may accept connections, as user or not.
fw=/usr/libexec/ApplicationFirewall/socketfilterfw
if [ "$FIREWALL" = 1 ] && "$fw" --getglobalstate 2>/dev/null | grep -q 'enabled'; then
  if [ "$MAC_SYSTEM" = 1 ]; then
    "$fw" --add "$prog" >/dev/null 2>&1 && "$fw" --unblockapp "$prog" >/dev/null 2>&1
    say "  Firewall: BitVoice may accept incoming connections."
  else
    say "  The Mac's firewall is on: when it asks whether BitVoice may accept incoming connections, allow it,"
    say "  so phones can connect (it asks again after an upgrade)."
  fi
fi
if ! wait_ready "$url"; then
  tail -n 30 "$logs/bitvoice.log" "$logs/launchd.log" 2>/dev/null || true
  fail "BitVoice did not start: see the log above"
fi
addr=$(ipconfig getifaddr "$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')" 2>/dev/null)
say ""
say "BitVoice $VERSION is running: open $url on this Mac."
if [ -n "$addr" ]; then
  [ "$port" = 443 ] && say "  From another computer or phone: https://$addr" || say "  From another computer or phone: https://$addr:$port"
fi
say "  The browser warns once about BitVoice's own certificate until you give it yours (Settings, Certificate)."
# At the Mac (not over SSH): open it in the browser the first time.
if [ "$had" = 0 ] && [ -z "${SSH_CONNECTION:-}" ] && [ -z "${BITVOICE_NO_BROWSER:-}" ]; then
  if [ "$MAC_SYSTEM" = 1 ] && [ -n "${SUDO_USER:-}" ]; then
    launchctl asuser "$(id -u "$SUDO_USER")" sudo -u "$SUDO_USER" open "$url" >/dev/null 2>&1 || true
  elif [ "$MAC_SYSTEM" = 0 ]; then
    open "$url" >/dev/null 2>&1 || true
  fi
fi
