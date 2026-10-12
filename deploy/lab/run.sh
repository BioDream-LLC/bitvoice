#!/bin/sh
# BitVoice cluster lab: two servers on separate addresses, one PostgreSQL, test phones.
#
#   ./run.sh up        build BitVoice for Linux, start the lab, run the setup, set the shared address
#   ./run.sh call      a call between two phones on different servers (sound both ways)
#   ./run.sh failover  a call through the shared address; the server carrying it is killed mid-call
#   ./run.sh impaired  a call from a phone on a bad link (80 ms +-30 ms delay, 5% loss): BitVoice must
#                      measure about 5% loss, not count late packets as lost
#   ./run.sh down      stop the lab and delete its data
#
# Needs Docker with Compose, Go and Python 3. Each test prints PASS or FAIL and the phones' logs.
set -eu
cd "$(dirname "$0")"
NET=bvlab_lab
SHARED=172.30.0.50
PW='a very good password'

say() { printf '\n== %s\n' "$*"; }
cookie=$(mktemp)
trap 'rm -f "$cookie"' EXIT
api() { # api PORT METHOD PATH [JSON]
	curl -s -b "$cookie" -c "$cookie" -H 'X-BitVoice: 1' -H 'Content-Type: application/json' -X "$2" ${4:+-d "$4"} "http://127.0.0.1:$1$3"
}
login() { api "$1" POST /api/auth/login "{\"username\":\"admin\",\"password\":\"$PW\"}" >/dev/null; }
alive() { for _ in $(seq 60); do curl -sf "http://127.0.0.1:$1/api/health/live" >/dev/null && return 0; sleep 1; done; echo "server on $1 did not start"; exit 1; }
secret() { login 18501; api 18501 GET /api/extensions | python3 -c "import json,sys; print([x['secret'] for x in json.load(sys.stdin)['items'] if x['number']=='$1'][0])"; }
phone() { # phone NAME IP EXT SERVER MODE [NUMBER SECONDS]
	docker rm -f "$1" >/dev/null 2>&1 || true
	docker run -d --name "$1" --network "$NET" --ip "$2" -v "$PWD:/lab:ro" python:3.12-slim \
		python3 -u /lab/labphone.py "$3" "$(secret "$3")" "$4" "$5" "${6:-}" "${7:-30}" >/dev/null
}
heard_after() { # packets a phone heard after its last re-INVITE
	docker logs "$1" 2>&1 | awk '/re-INVITE/{n=0; on=1} on && /heard/{n+=$4} END{print n+0}'
}

case "${1:-}" in
up)
	say "Building BitVoice for Linux"
	arch=$(docker info --format '{{.Architecture}}' | sed 's/x86_64/amd64/; s/aarch64/arm64/')
	(cd ../.. && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o deploy/lab/bitvoice ./cmd/bitvoice)
	docker compose build -q bv1 >/dev/null # one build: bv2 runs the same image
	docker compose up -d --wait db bv1 bv2 >/dev/null
	alive 18501; alive 18502
	if [ "$(api 18501 GET /api/setup/status | python3 -c 'import json,sys; print(json.load(sys.stdin)["complete"])')" = False ]; then
		say "Setting up (organisation Lab, extensions 100-102)"
		code=$(docker compose logs bv1 | grep -o 'another computer: [0-9]*' | tail -1 | grep -o '[0-9]*$')
		curl -s -o /dev/null -H 'X-BitVoice: 1' -H "X-Setup-Code: $code" -H 'Content-Type: application/json' \
			-d "{\"organisation\":\"Lab\",\"purpose\":\"office\",\"country\":\"us\",\"admin\":{\"username\":\"admin\",\"name\":\"Admin\",\"password\":\"$PW\"},\"extensions\":[{\"number\":\"100\",\"name\":\"Front desk\"},{\"number\":\"101\",\"name\":\"Dr Lee\"},{\"number\":\"102\",\"name\":\"Sam\"}],\"inbound\":\"all\"}" \
			http://127.0.0.1:18501/api/setup/complete
	fi
	login 18501
	say "Shared address $SHARED"
	api 18501 GET /api/settings | python3 -c "
import json,sys; s=json.load(sys.stdin); s['system'].setdefault('cluster',{}).update({'sharedAddress':'$SHARED/24'}); print(json.dumps(s))" >"$cookie.s"
	api 18501 PUT /api/settings "$(cat "$cookie.s")" >/dev/null; rm -f "$cookie.s"
	docker compose restart bv1 bv2 >/dev/null; alive 18501; alive 18502; sleep 4
	login 18501
	api 18501 GET /api/servers | python3 -c "
import json,sys; d=json.load(sys.stdin)
for n in d['nodes']: print('  ', n['name'], n['sipAddr'], 'running' if n['alive'] else 'stopped')
a=d.get('address') or {}; print('   shared address', a.get('address'), 'held by', a.get('holder') or 'nobody', a.get('error',''))"
	;;
call)
	say "Phone 101 on server 2, phone 100 on server 1; 100 calls 101 for 6 seconds"
	phone lab-p101 172.30.0.101 101 172.30.0.12 answer "" 12
	sleep 2
	phone lab-p100 172.30.0.100 100 172.30.0.11 call 101 6
	sleep 10
	a=$(docker logs lab-p100 2>&1 | awk '/heard/{n+=$4} END{print n+0}'); b=$(docker logs lab-p101 2>&1 | awk '/heard/{n+=$4} END{print n+0}')
	echo "   100 heard $a packets, 101 heard $b"
	[ "$a" -gt 200 ] && [ "$b" -gt 200 ] && echo PASS || { echo FAIL; docker logs lab-p100; docker logs lab-p101; }
	;;
failover)
	holder=$(login 18501; api 18501 GET /api/servers | python3 -c "
import json,sys; d=json.load(sys.stdin); h=(d.get('address') or {}).get('holder',''); print([n['id'] for n in d['nodes'] if n['name']==h][0] if h else '')")
	svc=bv1; [ "$(docker compose exec -T bv2 ip -4 addr show | grep -c "$SHARED")" -gt 0 ] && svc=bv2
	say "Both phones register at the shared address (held by $svc); 100 calls 101 for 20 seconds; $svc is killed after 6"
	phone lab-p101 172.30.0.101 101 "$SHARED" answer "" 40
	sleep 2
	phone lab-p100 172.30.0.100 100 "$SHARED" call 101 20
	sleep 6
	docker compose kill "$svc" >/dev/null; echo "   killed $svc at $(date +%T)"
	sleep 18
	a=$(heard_after lab-p100); b=$(heard_after lab-p101)
	echo "   after the takeover: 100 heard $a packets, 101 heard $b"
	docker logs lab-p100 2>&1 | grep -E 're-INVITE|hung up' | sed 's/^/   /'
	[ "$a" -gt 300 ] && [ "$b" -gt 300 ] && echo PASS || { echo FAIL; docker logs lab-p100 | tail -25; }
	docker compose up -d "$svc" >/dev/null
	;;
impaired)
	say "Phone 100 on a bad link (80 ms delay, 30 ms jitter, 5% loss) calls 101 for 20 seconds"
	phone lab-p101 172.30.0.101 101 172.30.0.11 answer "" 40
	docker rm -f lab-p100 >/dev/null 2>&1 || true
	docker run -d --name lab-p100 --cap-add NET_ADMIN --network "$NET" --ip 172.30.0.100 -v "$PWD:/lab:ro" python:3.12-slim sh -c \
		"apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq iproute2 >/dev/null 2>&1 && tc qdisc add dev eth0 root netem delay 80ms 30ms loss 5% && python3 -u /lab/labphone.py 100 '$(secret 100)' 172.30.0.11 call 101 20" >/dev/null
	sleep 45
	login 18501
	api 18501 GET '/api/cdr?limit=1' | python3 -c "
import json,sys; r=json.load(sys.stdin)['items'][0]; q=[x for x in r['quality'] if x['ext']=='100'][0]
pct=100*q['lost']/(q['packets']+q['lost']); print('   phone 100: %d received, %d lost (%.1f%%), jitter %.0f ms, MOS %.2f' % (q['packets'], q['lost'], pct, q['jitterMs'], q['mos']))
print('PASS' if 3 <= pct <= 8 else 'FAIL')"
	;;
down)
	docker rm -f lab-p100 lab-p101 >/dev/null 2>&1 || true
	docker compose down -v >/dev/null; rm -f bitvoice
	;;
*)
	sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
	;;
esac
