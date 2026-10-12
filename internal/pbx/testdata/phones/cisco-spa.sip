INVITE sip:*43@{{host}}:{{port}} SIP/2.0
Via: SIP/2.0/UDP {{me}};branch=z9hG4bK-{{rand}}
From: "Front Desk" <sip:100@{{host}}>;tag={{rand}}
To: <sip:*43@{{host}}>
Remote-Party-ID: "Front Desk" <sip:100@{{host}}>;screen=yes;party=calling
Call-ID: {{rand}}@{{me}}
CSeq: 101 INVITE
Max-Forwards: 70
Contact: "Front Desk" <sip:100@{{me}}>
Expires: 240
User-Agent: Cisco/SPA504G-7.6.2f
Content-Length: 0
Allow: ACK, BYE, CANCEL, INFO, INVITE, NOTIFY, OPTIONS, REFER, UPDATE
Supported: replaces
Content-Type: application/sdp

v=0
o=- 1234 1234 IN IP4 127.0.0.1
s=-
c=IN IP4 127.0.0.1
t=0 0
m=audio {{rtp}} RTP/AVP 0 2 8 18 101
a=rtpmap:0 PCMU/8000
a=rtpmap:2 G726-32/8000
a=rtpmap:8 PCMA/8000
a=rtpmap:18 G729a/8000
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-15
a=ptime:30
a=sendrecv
