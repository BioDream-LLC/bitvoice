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

# The demo's settings: the public name for Let's Encrypt, kept where the reset finds them.
mkdir -p /etc/bitvoice-demo
printf 'DOMAIN=%s\nEMAIL=%s\n' "$domain" "$email" > /etc/bitvoice-demo/env
chmod 0600 /etc/bitvoice-demo/env

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
chown -R bitvoice:bitvoice /var/lib/bitvoice
systemctl start bitvoice
/usr/local/bin/bitvoice-demo-seed -url https://127.0.0.1 -admin-password-file /root/bitvoice-demo-admin \
  -domain "$DOMAIN" -email "$EMAIL" -public-address "$(curl -fsS https://checkip.amazonaws.com)"
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
/usr/local/sbin/bitvoice-demo-reset
echo "demo ready at https://$domain (sign in as demo / try-bitvoice-demo)"
