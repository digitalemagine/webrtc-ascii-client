# Signaling

The client only receives video. It speaks three signaling protocols, chosen with `-adapter` or from the URL
(see [USAGE.md](USAGE.md)).

## WHEP (MediaMTX)

[WHEP](https://datatracker.ietf.org/doc/draft-ietf-wish-whep/) is WebRTC playback over plain HTTP, served
by MediaMTX, among others. Give the stream's WHEP URL; for MediaMTX that's `http://<host>:8889/<stream>/whep`.

```bash
webrtc-ascii-client http://localhost:8889/mystream/whep
```

- The client offers to receive video, waits up to 2 s for its ICE candidates, `POST`s the offer
  (`application/sdp`) and applies the answer.
- On exit it `DELETE`s the session URL from the `Location` header.
- Server errors end the connection with the status and message, e.g.
  `server answered 404 Not Found: … no stream is available on path 'mystream'`.

## Janus WebRTC Gateway

Uses the [Janus streaming plugin](https://janus.conf.meetecho.com/docs/streaming.html) to watch a
mountpoint; the session ID argument is the mountpoint ID.

```bash
webrtc-ascii-client ws://localhost:8188 1
```

- Connects with the `janus-protocol` WebSocket subprotocol that Janus requires.
- Numeric mountpoint IDs are sent as numbers, others as strings.
- Watch only, no publishing.
- Plugin errors end the connection with the plugin's message and code, e.g.
  `janus watch of stream 999 failed: No such mountpoint/stream 999 (code 455)`.

## Simple WebSocket protocol

A small JSON protocol for your own signaling server. The other side sends the offer, the client answers.

```bash
webrtc-ascii-client ws://localhost:8080/ws myroom
```

Every message is a JSON object with a `type` and the session ID given on the command line:

| `type` | Direction | Fields | Meaning |
|---|---|---|---|
| `join` | client → server | `sessionId` | First message after connecting: join this session |
| `offer` | server → client | `sdp` | SDP offer from the sending peer |
| `answer` | client → server | `sessionId`, `sdp` | The client's SDP answer |
| `ice` | both | `sessionId` (client → server), `ice` | One trickle ICE candidate |
| `ping` | client → server | `sessionId` | Application keepalive every 30 s; the server may ignore it |

```json
{"type": "join", "sessionId": "myroom"}
{"type": "offer", "sdp": {"type": "offer", "sdp": "v=0\r\n..."}}
{"type": "answer", "sessionId": "myroom", "sdp": {"type": "answer", "sdp": "v=0\r\n..."}}
{"type": "ice", "sessionId": "myroom", "ice": {"candidate": "candidate:1 1 udp 2130706431 192.0.2.1 5000 typ host", "sdpMid": "0", "sdpMLineIndex": 0, "usernameFragment": null}}
```

- `sdp` is a W3C `RTCSessionDescriptionInit` and `ice` an `RTCIceCandidateInit`: what `toJSON()` returns in a
  browser, so a browser peer can forward them unchanged.
- The end of candidate gathering is not signaled.
- The client also sends WebSocket ping frames every 30 s and stops reading signaling messages if no pong
  arrives for 120 s. Standard WebSocket servers answer pings automatically.
- Messages with another `type` are logged and ignored.

## Network

ICE uses the STUN servers given with `-stun` (default `stun:stun.l.google.com:19302`). On a LAN,
`-stun ""` skips STUN and uses local candidates only. There is no option for TURN
credentials, so TURN relays can't be used.
