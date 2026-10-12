INVITE sip:*43@{{host}};transport=UDP SIP/2.0
v: SIP/2.0/UDP {{me}};branch=z9hG4bK-524287-1---{{rand}};rport
Max-Forwards: 70
m: <sip:100@{{me}};transport=UDP>
t: <sip:*43@{{host}};transport=UDP>
f: <sip:100@{{host}};transport=UDP>;tag={{rand}}
i: {{rand}}
CSeq: 1 INVITE
Allow: INVITE, ACK, CANCEL, BYE, NOTIFY, REFER, MESSAGE, OPTIONS, INFO, SUBSCRIBE
c: application/sdp
Supported: replaces, norefersub, extended-refer, timer, sec-agree, outbound, path, X-cisco-serviceuri
User-Agent: Z 5.6.1 v2.10.20.2
Allow-Events: presence, kpml, talk, as-feature-event
l: 0

v=0
o=Z 0 1234 IN IP4 127.0.0.1
s=Z
c=IN IP4 127.0.0.1
t=0 0
m=audio {{rtp}} RTP/AVP 106 9 98 101 0 8 3
a=rtpmap:106 opus/48000/2
a=fmtp:106 sprop-maxcapturerate=16000; minptime=20; useinbandfec=1
a=rtpmap:98 telephone-event/48000
a=fmtp:98 0-16
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-16
a=sendrecv
a=rtcp-mux
