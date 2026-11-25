# Protocols

OpenSSM speaks three protocols on the API port (`9330`):

| Path | Protocol | Authentication | Used by |
|------|----------|----------------|---------|
| `POST /` | AWS JSON 1.1 (`X-Amz-Target: AmazonSSM.*`) | SigV4 (`ssm` service) | AWS CLI, SDKs |
| `POST /agent/v1/*` | JSON | Bearer instance token | `openssm-agent` |
| `GET /agent/v1/sessions/{id}` | WebSocket, `ssmmessages` framing | Bearer instance token | `openssm-agent` |
| `GET /v1/data-channel/{id}` | WebSocket, `ssmmessages` framing | `open_data_channel` token | `session-manager-plugin` |

## AWS JSON 1.1

```http
POST / HTTP/1.1
Content-Type: application/x-amz-json-1.1
X-Amz-Target: AmazonSSM.GetParameter
Authorization: AWS4-HMAC-SHA256 Credential=AKIA.../20260101/us-east-1/ssm/aws4_request, ...

{"Name": "/app/url", "WithDecryption": true}
```

Successful responses are `200` with `Content-Type: application/x-amz-json-1.1` and an `X-Amzn-RequestId` header.
Errors are described in [api-reference.md](api-reference.md#errors). Empty bodies are treated as `{}`.

### SigV4

The middleware rebuilds the canonical request (method, URI, query, signed headers, payload hash), derives the
signing key from the stored secret, and compares signatures in constant time. The credential scope must use the
service `ssm` and the configured region. `X-Amz-Content-Sha256`, when present, must match the body. The verified
credential's label becomes the caller name (used for `LastModifiedUser` and session owners).

## Agent API

All bodies are JSON. Non-2xx responses carry `{"code": "...", "message": "..."}`.

| Endpoint | Request | Response |
|----------|---------|----------|
| `POST /agent/v1/register` | `{activationId, activationCode, computerName, ipAddress, platformType, platformName, platformVersion, agentVersion}` | `{instanceId, token}` |
| `POST /agent/v1/heartbeat` | host info | `204` |
| `POST /agent/v1/poll` | `{waitSeconds}` (≤ 30) | `{commands: [...], cancellations: [...], sessions: [...]}` |
| `POST /agent/v1/commands/{commandId}` | `{status, plugins: [{name, status, responseCode, stdout, stderr, startTime, endTime}]}` | `204` |

The token is `<instanceId>.<secret>`; only a SHA-256 hash of the secret is stored. Polls double as heartbeats and
return immediately when work is queued. Each command payload contains the rendered document steps
(`name`, `action`, `inputs`, `precondition`, `timeoutSeconds`). Types live in
[`pkgs/v1/agentproto`](../pkgs/v1/agentproto/agentproto.go).

## Session Manager data channel

The data channel is wire compatible with
[aws/session-manager-plugin](https://github.com/aws/session-manager-plugin) (`src/message`, `src/datachannel`).

### Opening

1. The client connects to `StreamUrl` (`ws(s)://<host>/v1/data-channel/<SessionId>`).
2. It sends one **text** frame:
   ```json
   {"MessageSchemaVersion":"1.0","RequestId":"<uuid>","TokenValue":"<from StartSession>","ClientId":"<uuid>","ClientVersion":"1.3.0.0"}
   ```
3. All further frames are **binary** `ClientMessage`s. An invalid token closes the connection.

### ClientMessage

Big-endian; header length is always 116.

| Offset | Size | Field |
|--------|------|-------|
| 0 | 4 | HeaderLength (116) |
| 4 | 32 | MessageType, ASCII, space padded |
| 36 | 4 | SchemaVersion (1) |
| 40 | 8 | CreatedDate (epoch ms) |
| 48 | 8 | SequenceNumber |
| 56 | 8 | Flags |
| 64 | 16 | MessageId — UUID least-significant 8 bytes, then most-significant 8 bytes |
| 80 | 32 | PayloadDigest (SHA-256 of payload) |
| 112 | 4 | PayloadType |
| 116 | 4 | PayloadLength |
| 120 | n | Payload |

Message types: `input_stream_data` (client → agent), `output_stream_data` (agent → client), `acknowledge`,
`channel_closed`, `start_publication`, `pause_publication`.

Payload types: `1` Output, `2` Error, `3` Size (`{"cols","rows"}`), `4` Parameter, `5` HandshakeRequest,
`6` HandshakeResponse, `7` HandshakeComplete, `8`/`9` encryption challenge, `10` Flag (`2` = TerminateSession),
`11` StdErr, `12` ExitCode.

### Reliability

Each direction has its own sequence counter starting at 0. Every stream message is acknowledged with an
`acknowledge` message (`Flags=3`, JSON `{AcknowledgedMessageType, AcknowledgedMessageId,
AcknowledgedMessageSequenceNumber, IsSequentialMessage}`). Unacknowledged messages are retransmitted; receivers
buffer out-of-order messages and drop duplicates. [`ssmmessages.Stream`](../pkgs/v1/ssmmessages/stream.go)
implements this for both the relay and the agent.

### Handshake

Driven by the agent, exactly like `amazon-ssm-agent`:

1. Agent → client: `HandshakeRequest` `{"AgentVersion", "RequestedClientActions":[{"ActionType":"SessionType","ActionParameters":{"SessionType":"Standard_Stream","Properties":{}}}]}`
2. Client → agent: `HandshakeResponse` `{"ClientVersion", "ProcessedClientActions":[{"ActionType":"SessionType","ActionStatus":1}], "Errors":[]}`
3. Agent → client: `HandshakeComplete` `{"HandshakeTimeToComplete", "CustomerMessage"}`

After the handshake the client sends keystrokes (`Output` payloads) and terminal sizes (`Size`); the agent sends
PTY output. When the shell exits the agent sends `channel_closed` with an optional `Output` message, which the
relay forwards to the client.

### Relay

The server terminates both WebSockets of a session and runs an independent `Stream` on each, forwarding payloads.
If the client disconnects, the session stays alive for 60 s so the plugin can reconnect with `ResumeSession`
(outgoing sequence numbers and unacknowledged messages carry over). If the agent disconnects, the session ends.
