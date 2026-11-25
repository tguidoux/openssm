// Package ssmmessages implements the binary ClientMessage framing used by the
// AWS Session Manager data channel ("ssmmessages" service). It is wire
// compatible with aws/session-manager-plugin, so `aws ssm start-session` can
// talk to OpenSSM.
//
// Frame layout (big-endian):
//
//	offset size field
//	0      4    HeaderLength (= 116, offset of PayloadLength)
//	4      32   MessageType (ASCII, space padded)
//	36     4    SchemaVersion
//	40     8    CreatedDate (epoch millis)
//	48     8    SequenceNumber
//	56     8    Flags
//	64     16   MessageId (UUID: least-significant 8 bytes, then most-significant 8)
//	80     32   PayloadDigest (SHA-256 of payload)
//	112    4    PayloadType
//	116    4    PayloadLength
//	120    n    Payload
package ssmmessages

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Message types.
const (
	InputStreamData  = "input_stream_data"
	OutputStreamData = "output_stream_data"
	Acknowledge      = "acknowledge"
	ChannelClosed    = "channel_closed"
	StartPublication = "start_publication"
	PausePublication = "pause_publication"
)

// PayloadType values.
const (
	PayloadOutput            uint32 = 1
	PayloadError             uint32 = 2
	PayloadSize              uint32 = 3
	PayloadParameter         uint32 = 4
	PayloadHandshakeRequest  uint32 = 5
	PayloadHandshakeResponse uint32 = 6
	PayloadHandshakeComplete uint32 = 7
	PayloadEncChallengeReq   uint32 = 8
	PayloadEncChallengeResp  uint32 = 9
	PayloadFlag              uint32 = 10
	PayloadStdErr            uint32 = 11
	PayloadExitCode          uint32 = 12
)

// Flag payload values (PayloadFlag).
const (
	FlagDisconnectToPort   uint32 = 1
	FlagTerminateSession   uint32 = 2
	FlagConnectToPortError uint32 = 3
)

// Session types negotiated in the handshake.
const (
	SessionTypeShell          = "Standard_Stream"
	SessionTypeInteractive    = "InteractiveCommands"
	SessionTypeNonInteractive = "NonInteractiveCommands"
	SessionTypePort           = "Port"
)

const (
	headerLength       = 116
	messageTypeLength  = 32
	payloadDigestBytes = 32
	// MinFrameLength is the size of a frame with an empty payload.
	MinFrameLength = headerLength + 4
	// MaxPayload bounds accepted payload sizes.
	MaxPayload = 1 << 20
)

// UUID is a 16-byte message identifier.
type UUID [16]byte

// NewUUID returns a random version-4 UUID.
func NewUUID() UUID {
	var u UUID
	_, _ = rand.Read(u[:])
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return u
}

// String formats the UUID with hyphens.
func (u UUID) String() string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// Message is a decoded ClientMessage.
type Message struct {
	MessageType    string
	SchemaVersion  uint32
	CreatedDate    uint64
	SequenceNumber int64
	Flags          uint64
	MessageID      UUID
	PayloadType    uint32
	Payload        []byte
}

// Marshal encodes the message, computing the payload digest.
func (m *Message) Marshal() ([]byte, error) {
	if len(m.MessageType) > messageTypeLength {
		return nil, fmt.Errorf("message type %q too long", m.MessageType)
	}
	buf := make([]byte, MinFrameLength+len(m.Payload))
	binary.BigEndian.PutUint32(buf[0:], headerLength)
	copy(buf[4:36], bytes.Repeat([]byte{' '}, messageTypeLength))
	copy(buf[4:36], m.MessageType)
	binary.BigEndian.PutUint32(buf[36:], m.SchemaVersion)
	binary.BigEndian.PutUint64(buf[40:], m.CreatedDate)
	binary.BigEndian.PutUint64(buf[48:], uint64(m.SequenceNumber))
	binary.BigEndian.PutUint64(buf[56:], m.Flags)
	copy(buf[64:72], m.MessageID[8:16])
	copy(buf[72:80], m.MessageID[0:8])
	digest := sha256.Sum256(m.Payload)
	copy(buf[80:112], digest[:])
	binary.BigEndian.PutUint32(buf[112:], m.PayloadType)
	binary.BigEndian.PutUint32(buf[116:], uint32(len(m.Payload)))
	copy(buf[120:], m.Payload)
	return buf, nil
}

// Unmarshal decodes and validates a frame (including the payload digest).
func Unmarshal(data []byte) (*Message, error) {
	if len(data) < MinFrameLength {
		return nil, fmt.Errorf("frame too short: %d bytes", len(data))
	}
	hl := binary.BigEndian.Uint32(data[0:])
	if hl == 0 || int(hl)+4 > len(data) {
		return nil, fmt.Errorf("invalid header length %d", hl)
	}
	m := &Message{
		MessageType:    strings.TrimSpace(string(bytes.Trim(data[4:36], "\x00"))),
		SchemaVersion:  binary.BigEndian.Uint32(data[36:]),
		CreatedDate:    binary.BigEndian.Uint64(data[40:]),
		SequenceNumber: int64(binary.BigEndian.Uint64(data[48:])),
		Flags:          binary.BigEndian.Uint64(data[56:]),
		PayloadType:    binary.BigEndian.Uint32(data[112:]),
	}
	copy(m.MessageID[8:16], data[64:72])
	copy(m.MessageID[0:8], data[72:80])
	payloadLen := binary.BigEndian.Uint32(data[hl:])
	payload := data[hl+4:]
	if payloadLen > MaxPayload || int(payloadLen) != len(payload) {
		return nil, fmt.Errorf("payload length %d does not match frame (%d bytes)", payloadLen, len(payload))
	}
	if payloadLen > 0 {
		digest := sha256.Sum256(payload)
		if !bytes.Equal(digest[:], data[80:80+payloadDigestBytes]) {
			return nil, errors.New("payload digest mismatch")
		}
	}
	m.Payload = append([]byte(nil), payload...)
	return m, nil
}

// NewStreamMessage builds an input/output stream data message.
func NewStreamMessage(messageType string, seq int64, payloadType uint32, payload []byte) *Message {
	flags := uint64(0)
	if seq == 0 {
		flags = 1 // SYN on the first message, mirroring the agent
	}
	return &Message{
		MessageType: messageType, SchemaVersion: 1, CreatedDate: nowMillis(), SequenceNumber: seq,
		Flags: flags, MessageID: NewUUID(), PayloadType: payloadType, Payload: payload,
	}
}

// AcknowledgeContent is the JSON payload of an acknowledge message.
type AcknowledgeContent struct {
	MessageType         string `json:"AcknowledgedMessageType"`
	MessageID           string `json:"AcknowledgedMessageId"`
	SequenceNumber      int64  `json:"AcknowledgedMessageSequenceNumber"`
	IsSequentialMessage bool   `json:"IsSequentialMessage"`
}

// NewAcknowledge builds the acknowledge message for a received stream message.
func NewAcknowledge(of *Message) *Message {
	payload, _ := json.Marshal(AcknowledgeContent{
		MessageType: of.MessageType, MessageID: of.MessageID.String(), SequenceNumber: of.SequenceNumber, IsSequentialMessage: true,
	})
	return &Message{
		MessageType: Acknowledge, SchemaVersion: 1, CreatedDate: nowMillis(), Flags: 3,
		MessageID: NewUUID(), Payload: payload,
	}
}

// ChannelClosedContent is the JSON payload of a channel_closed message.
type ChannelClosedContent struct {
	MessageID     string `json:"MessageId"`
	CreatedDate   string `json:"CreatedDate"`
	DestinationID string `json:"DestinationId"`
	SessionID     string `json:"SessionId"`
	MessageType   string `json:"MessageType"`
	SchemaVersion int    `json:"SchemaVersion"`
	Output        string `json:"Output"`
}

// NewChannelClosed builds a channel_closed message.
func NewChannelClosed(sessionID, output string) *Message {
	id := NewUUID()
	payload, _ := json.Marshal(ChannelClosedContent{
		MessageID: id.String(), CreatedDate: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		SessionID: sessionID, MessageType: ChannelClosed, SchemaVersion: 1, Output: output,
	})
	return &Message{MessageType: ChannelClosed, SchemaVersion: 1, CreatedDate: nowMillis(), MessageID: id, Payload: payload}
}

// Handshake payloads.

// HandshakeRequest is sent by the agent to start the session.
type HandshakeRequest struct {
	AgentVersion           string                  `json:"AgentVersion"`
	RequestedClientActions []RequestedClientAction `json:"RequestedClientActions"`
}

// RequestedClientAction is one action in a HandshakeRequest.
type RequestedClientAction struct {
	ActionType       string          `json:"ActionType"`
	ActionParameters json.RawMessage `json:"ActionParameters"`
}

// SessionTypeRequest is the parameters of a SessionType action.
type SessionTypeRequest struct {
	SessionType string `json:"SessionType"`
	Properties  any    `json:"Properties"`
}

// HandshakeResponse is sent by the client in reply.
type HandshakeResponse struct {
	ClientVersion          string                  `json:"ClientVersion"`
	ProcessedClientActions []ProcessedClientAction `json:"ProcessedClientActions"`
	Errors                 []string                `json:"Errors"`
}

// ProcessedClientAction is the client's result for one action.
type ProcessedClientAction struct {
	ActionType   string `json:"ActionType"`
	ActionStatus int    `json:"ActionStatus"`
	ActionResult any    `json:"ActionResult"`
	Error        string `json:"Error"`
}

// HandshakeComplete tells the client the session is ready.
type HandshakeComplete struct {
	HandshakeTimeToComplete time.Duration `json:"HandshakeTimeToComplete"`
	CustomerMessage         string        `json:"CustomerMessage"`
}

// SizeData is the terminal size payload (PayloadSize).
type SizeData struct {
	Cols uint32 `json:"cols"`
	Rows uint32 `json:"rows"`
}

// OpenDataChannelInput is the first (text) frame sent by the client.
type OpenDataChannelInput struct {
	MessageSchemaVersion string `json:"MessageSchemaVersion"`
	RequestID            string `json:"RequestId"`
	TokenValue           string `json:"TokenValue"`
	ClientID             string `json:"ClientId"`
	ClientVersion        string `json:"ClientVersion"`
}

func nowMillis() uint64 { return uint64(time.Now().UnixMilli()) }
