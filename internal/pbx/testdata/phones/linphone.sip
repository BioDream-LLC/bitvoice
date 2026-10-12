INVITE sip:*43@{{host}}:{{port}} SIP/2.0
Via: SIP/2.0/UDP {{me}};branch=z9hG4bK.{{rand}};rport
From: <sip:100@{{host}}>;tag={{rand}}
To: sip:*43@{{host}}
CSeq: 20 INVITE
Call-ID: {{rand}}
Max-Forwards: 70
Supported: replaces, outbound, gruu, path
Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO, PRACK, UPDATE
Content-Type: application/sdp
Contact: <sip:100@{{me}};transport=udp>;expires=3600;+sip.instance="<urn:uuid:00000000-0000-0000-0000-000000000001>"
User-Agent: Linphone-Desktop/5.2.4 (belle-sip/5.3.0)
Content-Length: 0

v=0
o=100 1234 5678 IN IP4 127.0.0.1
s=Talk
c=IN IP4 127.0.0.1
t=0 0
a=ice-pwd:31ec21eb38b2ec6d36e8dc7b
a=ice-ufrag:7c6a
a=rtcp-xr:rcvr-rtt=all:10000 stat-summary=loss,dup,jitt,TTL voip-metrics
a=record:off
m=audio {{rtp}} RTP/AVP 96 97 98 0 8 101 99 100
c=IN IP4 127.0.0.1
a=rtpmap:96 opus/48000/2
a=fmtp:96 useinbandfec=1
a=rtpmap:97 speex/16000
a=rtpmap:98 speex/8000
a=rtpmap:101 telephone-event/48000
a=rtpmap:99 telephone-event/16000
a=rtpmap:100 telephone-event/8000
a=rtcp:{{rtcp}}
a=candidate:1 1 UDP 2130706431 127.0.0.1 {{rtp}} typ host
a=candidate:1 2 UDP 2130706430 127.0.0.1 {{rtcp}} typ host
a=rtcp-fb:* trr-int 1000
a=rtcp-fb:* ccm tmmbr
