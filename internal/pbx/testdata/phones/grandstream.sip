INVITE sip:*43@{{host}}:{{port}} SIP/2.0
Via: SIP/2.0/UDP {{me}};branch=z9hG4bK{{rand}};rport
Route: <sip:{{host}}:{{port}};lr>
From: "Front Desk" <sip:100@{{host}}>;tag={{rand}}
To: <sip:*43@{{host}}>
Call-ID: {{rand}}@{{me}}
CSeq: 20160 INVITE
Contact: <sip:100@{{me}};transport=udp>
Supported: replaces, path, timer, eventlist
User-Agent: Grandstream GRP2614 1.0.5.33
Privacy: none
P-Preferred-Identity: "Front Desk" <sip:100@{{host}}>
Allow: INVITE, ACK, OPTIONS, CANCEL, BYE, SUBSCRIBE, NOTIFY, INFO, REFER, UPDATE, MESSAGE
Session-Expires: 1800
Min-SE: 90
Content-Type: application/sdp
Accept: application/sdp, application/dtmf-relay
Max-Forwards: 70
Content-Length: 0

v=0
o=100 8000 8000 IN IP4 127.0.0.1
s=SIP Call
c=IN IP4 127.0.0.1
t=0 0
m=audio {{rtp}} RTP/AVP 0 8 9 18 101
a=sendrecv
a=rtpmap:0 PCMU/8000
a=ptime:20
a=rtpmap:8 PCMA/8000
a=rtpmap:9 G722/8000
a=rtpmap:18 G729/8000
a=fmtp:18 annexb=no
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-15
