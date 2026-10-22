#!/bin/sh
# provision.sh - makes (or updates) the public BitVoice demo from this machine. DO NOT run it without the
# owner's go-ahead: it creates a paid cloud server.
#
#   DOMAIN=demo.bitvoice.org VERSION=v0.1.8 [EMAIL=you@example.com] sh deploy/demo/provision.sh
#
# It creates an Amazon Lightsail instance of its own (Ubuntu, the smallest plan; BitVoice needs about
# 100 MB at 200 calls), a static IP, and opens only what a visitor's browser needs: 443 (the web
# interface and its browser phone), 80 (Let's Encrypt's check) and the calls' audio ports 10000-20000
# UDP. SIP (5060) stays closed, so no phone on the internet can register to it. Then it installs the
# release with deploy/install.sh, fills it with an invented business (deploy/demo/seed) and sets up the
# nightly reset. Run it again to upgrade to another VERSION; the reset rebuilds the data anyway.
#
# DNS for bitvoice.org is at Namecheap, not in AWS: add an A record for DOMAIN pointing at the static IP
# this prints, then run it again so Let's Encrypt can check the name.
# Needs the AWS CLI signed in, Go, and the SSH key in KEYS (default ~/keys), mode 600, never printed.
set -eu
: "${DOMAIN:?set DOMAIN, like demo.bitvoice.org}" "${VERSION:?set VERSION, a released version like v0.1.8}"
EMAIL=${EMAIL:-} # optional: where the certificate authority writes about the certificate
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
export AWS_PAGER=""
region=${REGION:-us-east-1}
name=bitvoice-demo
keys=${KEYS:-$HOME/keys}
key="$keys/lightsail-$region.pem"
[ -f "$key" ] || { echo "no SSH key at $key (Lightsail's default key for $region)" >&2; exit 1; }

if ! aws lightsail get-instance --region "$region" --instance-name $name >/dev/null 2>&1; then
  q='bundles[?isActive && supportedPlatforms[0]==`LINUX_UNIX` && publicIpv4AddressCount > `0`] | sort_by(@, &price)[0].bundleId'
  bundle=$(aws lightsail get-bundles --region "$region" --query "$q" --output text)
  aws lightsail create-instances --region "$region" --instance-names $name --availability-zone "${region}a" \
    --blueprint-id ubuntu_24_04 --bundle-id "$bundle" >/dev/null
fi
# The static IP: made once, and attached once the server runs (each step skipped when done).
aws lightsail get-static-ip --region "$region" --static-ip-name $name-ip >/dev/null 2>&1 ||
  aws lightsail allocate-static-ip --region "$region" --static-ip-name $name-ip >/dev/null
until [ "$(aws lightsail get-instance --region "$region" --instance-name $name --query instance.state.name --output text)" = running ]; do sleep 5; done
if [ "$(aws lightsail get-static-ip --region "$region" --static-ip-name $name-ip --query staticIp.isAttached --output text)" != True ]; then
  aws lightsail attach-static-ip --region "$region" --static-ip-name $name-ip --instance-name $name >/dev/null
fi
aws lightsail put-instance-public-ports --region "$region" --instance-name $name --port-infos \
  "fromPort=22,toPort=22,protocol=tcp" "fromPort=80,toPort=80,protocol=tcp" "fromPort=443,toPort=443,protocol=tcp" \
  "fromPort=10000,toPort=20000,protocol=udp" >/dev/null
ip=$(aws lightsail get-static-ip --region "$region" --static-ip-name $name-ip --query staticIp.ipAddress --output text)
echo "the demo server is $ip"
if [ "$(dig +short "$DOMAIN" @8.8.8.8 | tail -1)" != "$ip" ]; then
  echo "add an A record: $DOMAIN -> $ip (at Namecheap), then run this again" >&2
  exit 1
fi

build=$(mktemp -d)
trap 'rm -rf "$build"' EXIT
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -buildid=" -o "$build/bitvoice-demo-seed" ./deploy/demo/seed)
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -buildid=" -o "$build/bitvoice-demo-911" ./deploy/demo/caller)
cp "$root/deploy/install.sh" "$here/setup.sh" "$build/"
# The CAD this demo sends 911 calls to, if there is one: KEY=VALUE lines EIDO_URL, EIDO_TOKEN, RADIO_URL,
# RADIO_SECRET (https addresses), kept outside the repository.
cad=${CAD_ENV:-$keys/bitdispatcher-demo}
[ -f "$cad" ] && cp "$cad" "$build/cad.env"

ssh_opts="-i $key -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$keys/known_hosts -o ConnectTimeout=10"
until ssh $ssh_opts ubuntu@"$ip" true 2>/dev/null; do sleep 5; done
ssh $ssh_opts ubuntu@"$ip" 'rm -rf /tmp/bitvoice-demo && mkdir -p /tmp/bitvoice-demo'
scp -q $ssh_opts "$build"/* ubuntu@"$ip":/tmp/bitvoice-demo/
ssh $ssh_opts ubuntu@"$ip" "sudo sh /tmp/bitvoice-demo/setup.sh '$VERSION' '$DOMAIN' '$EMAIL'; rm -rf /tmp/bitvoice-demo"
# The token the CAD calls this demo with (admin, names the dispatcher it acts for): kept here for
# whoever sets up the CAD, never printed.
(umask 077 && ssh $ssh_opts ubuntu@"$ip" 'sudo cat /etc/bitvoice-demo/cad-token' > "$keys/bitvoice-demo-cad")
echo "the CAD's token for this demo is in $keys/bitvoice-demo-cad"
