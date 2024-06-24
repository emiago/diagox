// SPDX-License-Identifier: MPL-2.0
// SPDX-FileCopyrightText: Copyright (c) 2024, Emir Aganovic

package diagox

// func TestCacheLoad(t *testing.T) {

// 	data := `[
// 		{
// 		  "ID": "vNQxP8AV1UTrWIYOKtxPpc4-NzTSeOac__1d64be11-89f3-40c5-821c-8b75ef85e044__84dXoMjFU-X3cbg-920QgAfHt3Ca2eqU",
// 		  "NodeID": "127.0.0.1:9001",
// 		  "Direction": 0,
// 		  "InviteRequestData": "INVITE sip:playback@127.0.0.1:5061 SIP/2.0\r\nVia: SIP/2.0/UDP 192.168.100.3:52731;branch=z9hG4bKPjaRVgKEufXQIyVIczIcCDfumu6WPJa65G;rport\r\nMax-Forwards: 70\r\nFrom: <sip:alice@localhost>;tag=84dXoMjFU-X3cbg-920QgAfHt3Ca2eqU\r\nTo: <sip:playback@127.0.0.1>;tag=1d64be11-89f3-40c5-821c-8b75ef85e044\r\nContact: <sip:alice@127.0.0.1:52731;ob>\r\nCall-ID: vNQxP8AV1UTrWIYOKtxPpc4-NzTSeOac\r\nCSeq: 7142 INVITE\r\nk: replaces, 100rel, timer, norefersub\r\nx: 1800\r\nMin-SE: 90\r\nUser-Agent: PJSUA v2.13-dev Linux-6.8.5.201/x86_64/glibc-2.36\r\nContent-Type: application/sdp\r\nContent-Length: 558\r\n\r\nv=0\r\no=- 3935498773 3935498773 IN IP4 192.168.100.3\r\ns=pjmedia\r\nb=AS:84\r\nt=0 0\r\na=X-nat:0\r\nm=audio 58705 RTP/AVP 96 97 98 99 3 0 8 9 120 121 122\r\nc=IN IP4 192.168.100.3\r\nb=TIAS:64000\r\na=sendrecv\r\na=rtpmap:96 speex/16000\r\na=rtpmap:97 speex/8000\r\na=rtpmap:98 speex/32000\r\na=rtpmap:99 iLBC/8000\r\na=fmtp:99 mode=30\r\na=rtpmap:120 telephone-event/16000\r\na=fmtp:120 0-16\r\na=rtpmap:121 telephone-event/8000\r\na=fmtp:121 0-16\r\na=rtpmap:122 telephone-event/32000\r\na=fmtp:122 0-16\r\na=ssrc:866985782 cname:08ca1a250fc67926\r\na=rtcp:58706 IN IP4 192.168.100.3\r\na=rtcp-mux\r\n"
// 		}
// 	  ]
// 	  `
// 	buf := bytes.NewBuffer([]byte(data))
// 	dialogs, err := CacheLoadDialogs(buf, "")
// 	require.NoError(t, err)
// 	for _, d := range dialogs {
// 		require.NotNil(t, d.InviteRequest)
// 	}
// }
