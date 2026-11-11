# BioDream's push relay (push.bitvoice.org)

`bvpush` (cmd/bvpush) wakes the released phone apps for calls and chat. BitVoice servers set to
**Push relay** send it a signed message (HMAC-SHA256 with their own secret, five minutes' grace on the
clock); it passes the message to Google (and, once the iPhone app ships, Apple) with BioDream's
Firebase key, which never leaves it. Each server may send 20 pushes a second, in bursts of up to 100.
It keeps nothing: no tokens, no messages, no log of who called whom (only failed pushes are logged,
with the server's key ID and Google's reason).

## Where it runs

On the Lightsail server `bitdispatcher-demo` (us-east-1, static IP 32.194.53.140), beside the
bitdispatcher demo, behind the Caddy already there:

| What | Where |
|---|---|
| Program | `/usr/local/bin/bvpush` (linux/amd64, built with `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/bvpush`) |
| Service | `bvpush.service` (this folder), user `bvpush`, listening on 127.0.0.1:8095 |
| Server keys | `/etc/bvpush/servers.json` (root:bvpush 0640): `{"KEY-ID": "secret", ...}` |
| Firebase key | `/etc/bvpush/fcm.json` (root:bvpush 0640), the `bitvoice-c4778` service account |
| TLS | Caddy, block below; Let's Encrypt certificate for push.bitvoice.org |
| DNS | Namecheap: `A push` -> 32.194.53.140 |

```
# BitVoice push relay (bvpush.service)
push.bitvoice.org {
	reverse_proxy 127.0.0.1:8095
}
```

Moving it to a machine of its own: install the program, the service and the two files there, add the
same Caddy block, and change the DNS record. Servers keep their key IDs and secrets.

## Giving a server a key

On the relay's machine: `/usr/local/sbin/bvpush-server customer-name` (this folder's add-server.sh, installed as /usr/local/sbin/bvpush-server) prints the key ID and a new secret, once. They
go into that server's Settings, **Mobile apps**, **Push relay** (address https://push.bitvoice.org).
`/usr/local/sbin/bvpush-server -r customer-name` revokes a key at once (its server's pushes are refused with 401).

## Checking it

- `curl https://push.bitvoice.org/health` answers `ok`.
- An unsigned or wrongly signed POST to `/v1/push` gets 401; a signed one with a made-up token gets 502,
  and `journalctl -u bvpush` shows Google's reason ("not a valid FCM registration token"): the Firebase
  key works.
- Field-tested 2026-11-11 with the Pixel 8: demo.bitvoice.org set to the relay, app process killed and
  screen off, a call to its extension woke it on the lock screen and was answered.
