# Fake SIP provider for the lab: accepts any REGISTER (no password), answers any INVITE with sound,
# and logs where each came from, to show which BitVoice server holds the line.
import re, socket, time, threading
sip = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); sip.bind(('0.0.0.0', 5060))
rtp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); rtp.bind(('0.0.0.0', 40000)); rtp.settimeout(0.2)
me = socket.gethostbyname(socket.gethostname()); media = {'to': None, 'heard': 0}
def log(*a): print('[provider', time.strftime('%H:%M:%S') + ']', *a, flush=True)
def hdr(m, k):
    r = re.search(r'^' + k + r':\s*(.*)$', m, re.M | re.I); return r.group(1).strip() if r else ''
def reply(m, a, code, reason, body='', extra=''):
    vias = ''.join(f'Via: {v}\r\n' for v in re.findall(r'^Via:\s*(.*)$', m, re.M))
    to = hdr(m, 'To') + ('' if ';tag=' in hdr(m, 'To') else ';tag=prov')
    sip.sendto((f'SIP/2.0 {code} {reason}\r\n{vias}From: {hdr(m,"From")}\r\nTo: {to}\r\nCall-ID: {hdr(m,"Call-ID")}\r\n'
                f'CSeq: {hdr(m,"CSeq")}\r\nContact: <sip:prov@{me}:5060>\r\n{extra}' + ('Content-Type: application/sdp\r\n' if body else '')
                + f'Content-Length: {len(body)}\r\n\r\n{body}').encode(), a)
def sound():
    pkt = bytearray(172); pkt[0] = 0x80; last = time.time()
    while True:
        if media['to']:
            seq = (pkt[2] << 8 | pkt[3]) + 1 & 0xffff; pkt[2], pkt[3] = seq >> 8, seq & 0xff
            ts = (int.from_bytes(pkt[4:8], 'big') + 160) & 0xffffffff; pkt[4:8] = ts.to_bytes(4, 'big'); pkt[8:12] = b'prov'
            rtp.sendto(bytes(pkt), media['to'])
        try: rtp.recvfrom(2048); media['heard'] += 1
        except socket.timeout: pass
        if time.time() - last >= 2 and media['to']:
            log('heard', media['heard'], 'packets in 2 s from the call'); media['heard'] = 0; last = time.time()
        time.sleep(0.02)
threading.Thread(target=sound, daemon=True).start()
while True:
    d, a = sip.recvfrom(65535); m = d.decode(errors='replace')
    if not m.strip() or m.startswith('SIP/2.0'): continue
    method = m.split()[0]
    if method == 'REGISTER':
        log('REGISTER from', a[0], 'expires', hdr(m, 'Expires') or re.search(r'expires=(\d+)', hdr(m, 'Contact')) and re.search(r'expires=(\d+)', hdr(m, 'Contact')).group(1))
        reply(m, a, 200, 'OK', extra=f'Expires: {hdr(m, "Expires") or 60}\r\n')
    elif method == 'INVITE':
        body = m.split('\r\n\r\n', 1)[1]; c = re.search(r'c=IN IP4 (\S+)', body); p = re.search(r'm=audio (\d+)', body)
        if c and p: media['to'] = (c.group(1), int(p.group(1)))
        log('INVITE from', a[0], 'to', m.split()[1], 'media', media['to'])
        reply(m, a, 200, 'OK', f'v=0\r\no=prov 1 1 IN IP4 {me}\r\ns=-\r\nc=IN IP4 {me}\r\nt=0 0\r\nm=audio 40000 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n')
    elif method == 'BYE':
        log('BYE from', a[0]); media['to'] = None; reply(m, a, 200, 'OK')
    elif method != 'ACK':
        reply(m, a, 200, 'OK')
