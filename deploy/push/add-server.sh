#!/bin/sh
# bvpush-server KEY-ID: gives one BitVoice server a key for the push relay, on the relay's machine.
# Prints the key ID and a new secret once, to enter in that server's Settings, Mobile apps, Push relay.
# bvpush-server -r KEY-ID revokes a server's key. Both reload the relay without a restart.
set -eu
F=/etc/bvpush/servers.json
revoke=
[ "${1:-}" = "-r" ] && { revoke=1; shift; }
id=${1:?usage: bvpush-server [-r] KEY-ID}
case $id in *[!A-Za-z0-9._-]*) echo "key IDs are letters, digits, dot, dash and underscore" >&2; exit 2 ;; esac
secret=$(head -c 36 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')
sudo python3 - "$F" "$id" "$secret" "$revoke" <<'EOF'
import json, os, sys
f, key, secret, revoke = sys.argv[1:]
keys = json.load(open(f))
if revoke:
    if keys.pop(key, None) is None:
        sys.exit("no such key: " + key)
elif key in keys:
    sys.exit("that key ID is taken; revoke it first (-r) to give it a new secret")
else:
    keys[key] = secret
tmp = f + ".new"
with open(tmp, "w") as out:
    json.dump(keys, out, indent=2)
    out.write("\n")
os.chown(tmp, 0, os.stat(f).st_gid)
os.chmod(tmp, 0o640)
os.replace(tmp, f)
EOF
sudo systemctl reload bvpush
if [ -n "$revoke" ]; then
	echo "revoked $id"
else
	printf 'Relay address: https://push.bitvoice.org\nKey ID: %s\nSecret: %s\n' "$id" "$secret"
fi
