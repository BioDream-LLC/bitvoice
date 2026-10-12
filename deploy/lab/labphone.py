# Lab phone: a SIP phone for testing BitVoice over a real network (not part of BitVoice).
# python3 labphone.py EXT SECRET SERVER_IP answer|call [NUMBER] [SECONDS]
# It registers, then answers the first call (or calls NUMBER), sends a tone packet every 20 ms,
# follows re-INVITEs (new media address, new Contact) and prints what it heard, once a second.
import hashlib, random, re, socket, sys, threading, time

ext, secret, server, mode = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
number = sys.argv[5] if len(sys.argv) > 5 else ''
seconds = float(sys.argv[6]) if len(sys.argv) > 6 else 30
SIP = (server, 5060)
me_ip = socket.gethostbyname(socket.gethostname())
sip = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); sip.bind(('0.0.0.0', 5060)); sip.settimeout(0.5)
rtp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); rtp.bind(('0.0.0.0', 40000)); rtp.settimeout(0.2)
me = f'{me_ip}:5060'
lock = threading.Lock()
state = {'media': None, 'heard': 0, 'from': set(), 'dialog': None, 'target': None, 'peer': SIP, 'cseq': 1, 'done': False}

def log(*a): print(f'[{ext} {time.strftime("%H:%M:%S")}]', *a, flush=True)
def hdr(m, k):
    r = re.search(r'^' + k + r':\s*(.*)$', m, re.M | re.I); return r.group(1).strip() if r else ''
def vias(m): return ''.join(f'Via: {v}\r\n' for v in re.findall(r'^Via:\s*(.*)$', m, re.M))
def branch(): return 'z9hG4bK%x' % random.getrandbits(48)
def sdp():
    return (f'v=0\r\no=lab 1 1 IN IP4 {me_ip}\r\ns=-\r\nc=IN IP4 {me_ip}\r\nt=0 0\r\n'
            f'm=audio 40000 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\na=sendrecv\r\n')
def media_of(body):
    c = re.search(r'c=IN IP4 (\S+)', body); m = re.search(r'm=audio (\d+)', body)
    return (c.group(1), int(m.group(1))) if c and m else None
def digest(ch, method, uri):
    realm = re.search(r'realm="([^"]+)"', ch).group(1); nonce = re.search(r'nonce="([^"]+)"', ch).group(1)
    h1 = hashlib.md5(f'{ext}:{realm}:{secret}'.encode()).hexdigest(); h2 = hashlib.md5(f'{method}:{uri}'.encode()).hexdigest()
    return (f'Digest username="{ext}", realm="{realm}", nonce="{nonce}", uri="{uri}", '
            f'response="{hashlib.md5(f"{h1}:{nonce}:{h2}".encode()).hexdigest()}", algorithm=MD5')

pending = {}  # CSeq -> [event, final response], for requests this phone sends
def request(method, uri, headers, body='', auth_header=''):
    """Send a request (answering one digest challenge); return the final response."""
    for attempt in range(2):
        with lock:
            state['cseq'] += 1; seq = state['cseq']
        ev = threading.Event(); pending[seq] = [ev, None]
        msg = (f'{method} {uri} SIP/2.0\r\nVia: SIP/2.0/UDP {me};branch={branch()};rport\r\n{headers}'
               f'CSeq: {seq} {method}\r\nContact: <sip:{ext}@{me}>\r\n{auth_header}'
               + ('Content-Type: application/sdp\r\n' if body else '') + f'Content-Length: {len(body)}\r\n\r\n{body}')
        sip.sendto(msg.encode(), SIP if method in ('REGISTER', 'INVITE') else state['peer'])
        ev.wait(8); r = pending.pop(seq)[1]
        if r and r.split()[1] in ('401', '407') and attempt == 0:
            name = 'WWW-Authenticate' if r.split()[1] == '401' else 'Proxy-Authenticate'
            auth_header = ('Authorization' if r.split()[1] == '401' else 'Proxy-Authorization') + ': ' + digest(hdr(r, name), method, uri) + '\r\n'
            continue
        return r
    return None

def reply(m, a, code, reason, body=''):
    to = hdr(m, 'To')
    if ';tag=' not in to: to += ';tag=lab' + ext
    sip.sendto((f'SIP/2.0 {code} {reason}\r\n{vias(m)}From: {hdr(m,"From")}\r\nTo: {to}\r\nCall-ID: {hdr(m,"Call-ID")}\r\n'
                f'CSeq: {hdr(m,"CSeq")}\r\nContact: <sip:{ext}@{me}>\r\n'
                + ('Content-Type: application/sdp\r\n' if body else '') + f'Content-Length: {len(body)}\r\n\r\n{body}').encode(), a)

def contact_of(m):
    ct = re.search(r'<([^>]+)>', hdr(m, 'Contact')); return ct.group(1) if ct else None

def sip_loop():
    while not state['done']:
        try: d, a = sip.recvfrom(65535)
        except socket.timeout: continue
        m = d.decode(errors='replace')
        if not m.strip(): continue  # keep-alive
        if m.startswith('SIP/2.0'):
            seq = int(hdr(m, 'CSeq').split()[0]); code = int(m.split()[1])
            if seq in pending and code >= 200: pending[seq][1] = m; pending[seq][0].set()
            continue
        method = m.split()[0]; body = m.split('\r\n\r\n', 1)[1] if '\r\n\r\n' in m else ''
        if method == 'INVITE' and ';tag=' in hdr(m, 'To'):
            with lock:
                state['media'] = media_of(body) or state['media']
                state['target'] = contact_of(m) or state['target']
                state['peer'] = a  # in-dialog requests go where this one came from from now on
            log('re-INVITE from', a[0], '-> media now', state['media'])
            reply(m, a, 200, 'OK', sdp())
        elif method == 'INVITE':
            with lock:
                state['media'] = media_of(body); state['peer'] = a; state['target'] = contact_of(m)
                state['dialog'] = {'callid': hdr(m, 'Call-ID'), 'from': hdr(m, 'To') + ';tag=lab' + ext, 'to': hdr(m, 'From')}
            log('ringing from', hdr(m, 'From').split(';')[0], 'via', a[0]); reply(m, a, 200, 'OK', sdp())
        elif method == 'BYE':
            reply(m, a, 200, 'OK'); log('BYE from', a[0]); state['done'] = True
        elif method != 'ACK':
            reply(m, a, 200, 'OK')

def rtp_send():
    pkt = bytearray(172); pkt[0] = 0x80; seq = 0; nxt = time.time()
    while not state['done']:
        with lock: target = state['media']
        if target:
            seq = (seq + 1) & 0xffff; pkt[2], pkt[3] = seq >> 8, seq & 0xff
            rtp.sendto(bytes(pkt), target)
        nxt += 0.02; time.sleep(max(0, nxt - time.time()))  # exactly 50 a second

def rtp_loop():
    threading.Thread(target=rtp_send, daemon=True).start()
    last = time.time()
    while not state['done']:
        try:
            d, a = rtp.recvfrom(2048)
            with lock: state['heard'] += 1; state['from'].add(f'{a[0]}:{a[1]}')
        except socket.timeout: pass
        if time.time() - last >= 1:
            with lock:
                log('heard', state['heard'], 'packets from', sorted(state['from'])); state['heard'] = 0; state['from'] = set()
            last = time.time()

threading.Thread(target=sip_loop, daemon=True).start()
r = request('REGISTER', f'sip:{server}', f'From: <sip:{ext}@{server}>;tag=r{ext}\r\nTo: <sip:{ext}@{server}>\r\nCall-ID: reg{ext}@lab\r\nExpires: 300\r\n')
log('register', r.split('\r\n')[0] if r else 'no answer')
threading.Thread(target=rtp_loop, daemon=True).start()
if mode == 'call':
    callid = f'call{random.getrandbits(32):x}@lab'
    hs = f'From: <sip:{ext}@{server}>;tag=c{ext}\r\nTo: <sip:{number}@{server}>\r\nCall-ID: {callid}\r\n'
    r = request('INVITE', f'sip:{number}@{server}', hs, sdp())
    log('call', r.split('\r\n')[0] if r else 'no answer')
    if r and r.split()[1] == '200':
        with lock:
            state['media'] = media_of(r.split('\r\n\r\n', 1)[1])
            state['target'] = contact_of(r) or f'sip:{number}@{server}'
            state['dialog'] = {'callid': callid, 'from': hdr(r, 'From'), 'to': hdr(r, 'To')}
        sip.sendto((f'ACK {state["target"]} SIP/2.0\r\nVia: SIP/2.0/UDP {me};branch={branch()}\r\nFrom: {hdr(r,"From")}\r\nTo: {hdr(r,"To")}\r\n'
                    f'Call-ID: {callid}\r\nCSeq: {hdr(r,"CSeq").split()[0]} ACK\r\nContent-Length: 0\r\n\r\n').encode(), state['peer'])
end = time.time() + seconds
while time.time() < end and not state['done']: time.sleep(0.2)
if not state['done'] and state['dialog']:
    d = state['dialog']
    r = request('BYE', state['target'], f'From: {d["from"]}\r\nTo: {d["to"]}\r\nCall-ID: {d["callid"]}\r\n')
    log('hung up:', r.split('\r\n')[0] if r else 'no answer')
state['done'] = True; time.sleep(0.3)

