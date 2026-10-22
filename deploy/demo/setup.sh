#!/bin/sh
# setup.sh - runs ON the demo server (as root, from provision.sh): installs BitVoice from its release with
# deploy/install.sh, then the nightly reset that rebuilds the invented business from nothing.
#   sh setup.sh VERSION DOMAIN EMAIL
set -eu
version=$1 domain=$2 email=$3
here=$(cd "$(dirname "$0")" && pwd)

# The program, as anyone installs it (checked against SHA256SUMS and the release signature).
BITVOICE_REQUIRE_SIGNATURE=1 sh "$here/install.sh" --version "$version" --no-firewall
install -m 0755 "$here/bitvoice-demo-seed" /usr/local/bin/bitvoice-demo-seed
install -m 0755 "$here/bitvoice-demo-911" /usr/local/bin/bitvoice-demo-911

# The demo's settings: the public name for Let's Encrypt, kept where the reset finds them.
mkdir -p /etc/bitvoice-demo
chmod 0700 /etc/bitvoice-demo
printf 'DOMAIN=%s\nEMAIL=%s\n' "$domain" "$email" > /etc/bitvoice-demo/env
chmod 0600 /etc/bitvoice-demo/env
# The 911 side's secrets, made once and kept across upgrades and nightly rebuilds: the demo
# call-taker's SIP password, and the token the CAD calls BitVoice with.
umask 077
[ -s /etc/bitvoice-demo/bot-secret ] || od -An -tx1 -N16 /dev/urandom | tr -d ' \n' > /etc/bitvoice-demo/bot-secret
[ -s /etc/bitvoice-demo/cad-token ] || printf 'bv_%s' "$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')" > /etc/bitvoice-demo/cad-token
[ -s /etc/bitvoice-demo/radio-token ] || printf 'bv_%s' "$(od -An -tx1 -N32 /dev/urandom | tr -d ' \n')" > /etc/bitvoice-demo/radio-token
# The CAD's addresses and the secrets BitVoice sends it (EIDO_URL, EIDO_TOKEN, RADIO_URL, RADIO_SECRET).
[ -f "$here/cad.env" ] && install -m 0600 "$here/cad.env" /etc/bitvoice-demo/cad.env
umask 022

# The invented 911 calls (an ESInet stand-in, the call-taker's phone and the speech stand-in).
cat > /etc/systemd/system/bitvoice-demo-911.service <<'UNIT'
[Unit]
Description=BitVoice demo: invented 911 calls and radio traffic
After=bitvoice.service
[Service]
ExecStart=/usr/local/bin/bitvoice-demo-911
LoadCredential=bot-secret:/etc/bitvoice-demo/bot-secret
LoadCredential=radio-token:/etc/bitvoice-demo/radio-token
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
Restart=always
RestartSec=10
[Install]
WantedBy=multi-user.target
UNIT

cat > /usr/local/sbin/bitvoice-demo-reset <<'RESET'
#!/bin/sh
# Every night: the demo starts again from nothing, so whatever visitors did is gone.
set -eu
. /etc/bitvoice-demo/env
systemctl stop bitvoice
# The certificate is kept (Let's Encrypt limits how often one is issued); everything else goes.
keep=$(mktemp -d)
[ -d /var/lib/bitvoice/tls ] && cp -a /var/lib/bitvoice/tls "$keep/"
rm -rf /var/lib/bitvoice/*
[ -d "$keep/tls" ] && cp -a "$keep/tls" /var/lib/bitvoice/
rm -rf "$keep"
# The CAD's token outlives the rebuild: it goes into the new data before the server starts.
/usr/local/bin/bitvoice-demo-seed -prepare-data /var/lib/bitvoice -cad-token-file /etc/bitvoice-demo/cad-token \
  -radio-token-file /etc/bitvoice-demo/radio-token
chown -R bitvoice:bitvoice /var/lib/bitvoice
systemctl start bitvoice
/usr/local/bin/bitvoice-demo-seed -url https://127.0.0.1 -admin-password-file /root/bitvoice-demo-admin \
  -domain "$DOMAIN" -email "$EMAIL" -public-address "$(curl -fsS https://checkip.amazonaws.com)" \
  -psap -bot-secret-file /etc/bitvoice-demo/bot-secret -cad-env /etc/bitvoice-demo/cad.env
RESET
chmod 0755 /usr/local/sbin/bitvoice-demo-reset

cat > /etc/systemd/system/bitvoice-demo-reset.service <<'UNIT'
[Unit]
Description=Rebuild the BitVoice demo from nothing
After=network-online.target bitvoice.service
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/bitvoice-demo-reset
UNIT
cat > /etc/systemd/system/bitvoice-demo-reset.timer <<'UNIT'
[Unit]
Description=Rebuild the BitVoice demo every night
[Timer]
OnCalendar=*-*-* 04:00:00 UTC
Persistent=true
[Install]
WantedBy=timers.target
UNIT
systemctl daemon-reload
systemctl enable --now bitvoice-demo-reset.timer >/dev/null
# The old demo program stops first: an upgrade's rebuild must not take its old calls or radio units.
systemctl stop bitvoice-demo-911.service 2>/dev/null || true
/usr/local/sbin/bitvoice-demo-reset
systemctl enable bitvoice-demo-911.service >/dev/null
systemctl restart bitvoice-demo-911.service
echo "demo ready at https://$domain (sign in as demo / try-bitvoice-demo)"
