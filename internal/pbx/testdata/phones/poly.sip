INVITE sip:*43@{{host}}:{{port}};user=phone SIP/2.0
Via: SIP/2.0/UDP {{me}};branch=z9hG4bK{{rand}};rport
From: "Front Desk" <sip:100@{{host}}>;tag={{rand}}
To: <sip:*43@{{host}};user=phone>
CSeq: 1 INVITE
Call-ID: {{rand}}
Contact: <sip:100@{{me}}>
Allow: INVITE, ACK, BYE, CANCEL, OPTIONS, INFO, MESSAGE, SUBSCRIBE, NOTIFY, PRACK, UPDATE, REFER
User-Agent: PolycomVVX-VVX_450-UA/6.4.3.5018
Accept-Language: en
Supported: 100rel,replaces
Allow-Events: conference,talk,hold
Max-Forwards: 70
Content-Type: application/sdp
Content-Length: 0

v=0
o=- 1700000000 1700000000 IN IP4 127.0.0.1
s=Polycom IP Phone
c=IN IP4 127.0.0.1
t=0 0
a=sendrecv
m=audio {{rtp}} RTP/AVP 9 102 0 8 127
a=rtpmap:9 G722/8000
a=rtpmap:102 G7221/16000
a=fmtp:102 bitrate=32000
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=rtpmap:127 telephone-event/8000
