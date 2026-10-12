INVITE sip:*43@{{host}}:{{port}} SIP/2.0
Via: SIP/2.0/UDP {{me}};branch=z9hG4bK{{rand}};rport
From: "Front Desk" <sip:100@{{host}}:{{port}}>;tag={{rand}}
To: <sip:*43@{{host}}:{{port}}>
Call-ID: {{rand}}@{{me}}
CSeq: 1 INVITE
Contact: <sip:100@{{me}}>
Content-Type: application/sdp
Allow: INVITE, INFO, PRACK, ACK, BYE, CANCEL, OPTIONS, NOTIFY, REGISTER, SUBSCRIBE, REFER, PUBLISH, UPDATE, MESSAGE
Max-Forwards: 70
User-Agent: Yealink SIP-T54W 96.86.0.70
Allow-Events: talk,hold,conference,refer,check-sync
Supported: replaces
Content-Length: 0

v=0
o=- 20041 20041 IN IP4 127.0.0.1
s=SDP data
c=IN IP4 127.0.0.1
t=0 0
m=audio {{rtp}} RTP/AVP 9 0 8 18 101
a=rtpmap:9 G722/8000
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=rtpmap:18 G729/8000
a=fmtp:18 annexb=no
a=ptime:20
a=sendrecv
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-15
