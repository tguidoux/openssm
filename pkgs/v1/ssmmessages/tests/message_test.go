package ssmmessages_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tguidoux/openssm/pkgs/v1/ssmmessages"
)

func TestMarshalLayoutMatchesSessionManagerPlugin(t *testing.T) {
	id := ssmmessages.UUID{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	m := &ssmmessages.Message{
		MessageType: ssmmessages.InputStreamData, SchemaVersion: 1, CreatedDate: 1700000000000,
		SequenceNumber: 42, Flags: 1, MessageID: id, PayloadType: ssmmessages.PayloadOutput, Payload: []byte("ls\r"),
	}
	b, err := m.Marshal()
	require.NoError(t, err)
	require.Len(t, b, 120+3)

	assert.Equal(t, uint32(116), binary.BigEndian.Uint32(b[0:4]), "HeaderLength")
	assert.Equal(t, "input_stream_data               ", string(b[4:36]), "MessageType is space padded to 32 bytes")
	assert.Equal(t, uint32(1), binary.BigEndian.Uint32(b[36:40]))
	assert.Equal(t, uint64(1700000000000), binary.BigEndian.Uint64(b[40:48]))
	assert.Equal(t, uint64(42), binary.BigEndian.Uint64(b[48:56]))
	assert.Equal(t, uint64(1), binary.BigEndian.Uint64(b[56:64]))
	// UUID is written least-significant half first.
	assert.Equal(t, []byte{8, 9, 10, 11, 12, 13, 14, 15}, b[64:72])
	assert.Equal(t, []byte{0, 1, 2, 3, 4, 5, 6, 7}, b[72:80])
	digest := sha256.Sum256([]byte("ls\r"))
	assert.Equal(t, digest[:], b[80:112])
	assert.Equal(t, uint32(1), binary.BigEndian.Uint32(b[112:116]))
	assert.Equal(t, uint32(3), binary.BigEndian.Uint32(b[116:120]))
	assert.Equal(t, "ls\r", string(b[120:]))

	back, err := ssmmessages.Unmarshal(b)
	require.NoError(t, err)
	assert.Equal(t, m, back)
	assert.Equal(t, "00010203-0405-0607-0809-0a0b0c0d0e0f", back.MessageID.String())
}

func TestUnmarshalRejectsCorruptFrames(t *testing.T) {
	m := ssmmessages.NewStreamMessage(ssmmessages.OutputStreamData, 0, ssmmessages.PayloadOutput, []byte("hello"))
	b, err := m.Marshal()
	require.NoError(t, err)

	_, err = ssmmessages.Unmarshal(b[:50])
	assert.Error(t, err)

	bad := append([]byte(nil), b...)
	bad[len(bad)-1] ^= 0xff
	_, err = ssmmessages.Unmarshal(bad)
	assert.ErrorContains(t, err, "digest")

	bad = append([]byte(nil), b...)
	binary.BigEndian.PutUint32(bad[116:], 99)
	_, err = ssmmessages.Unmarshal(bad)
	assert.Error(t, err)
}

func TestAcknowledgeAndChannelClosed(t *testing.T) {
	m := ssmmessages.NewStreamMessage(ssmmessages.OutputStreamData, 7, ssmmessages.PayloadOutput, []byte("x"))
	ack := ssmmessages.NewAcknowledge(m)
	assert.Equal(t, ssmmessages.Acknowledge, ack.MessageType)
	assert.Equal(t, uint64(3), ack.Flags)
	var content ssmmessages.AcknowledgeContent
	require.NoError(t, json.Unmarshal(ack.Payload, &content))
	assert.Equal(t, int64(7), content.SequenceNumber)
	assert.Equal(t, m.MessageID.String(), content.MessageID)
	assert.Equal(t, "output_stream_data", content.MessageType)

	cc := ssmmessages.NewChannelClosed("sess-1", "bye")
	var closed ssmmessages.ChannelClosedContent
	require.NoError(t, json.Unmarshal(cc.Payload, &closed))
	assert.Equal(t, "sess-1", closed.SessionID)
	assert.Equal(t, "bye", closed.Output)
	assert.Equal(t, "channel_closed", closed.MessageType)
}

// pipeConn is an in-memory Conn pair with optional frame dropping.
type pipeConn struct {
	in     chan []byte
	out    chan []byte
	drop   func([]byte) bool
	once   sync.Once
	closed chan struct{}
}

func newPipe() (*pipeConn, *pipeConn) {
	a, b := make(chan []byte, 1024), make(chan []byte, 1024)
	return &pipeConn{in: a, out: b, closed: make(chan struct{})}, &pipeConn{in: b, out: a, closed: make(chan struct{})}
}

func (p *pipeConn) WriteFrame(d []byte) error {
	if p.drop != nil && p.drop(d) {
		return nil
	}
	select {
	case p.out <- append([]byte(nil), d...):
		return nil
	case <-p.closed:
		return errors.New("closed")
	}
}

func (p *pipeConn) ReadFrame() ([]byte, error) {
	select {
	case d := <-p.in:
		return d, nil
	case <-p.closed:
		return nil, errors.New("closed")
	}
}

func (p *pipeConn) Close() error { p.once.Do(func() { close(p.closed) }); return nil }

func TestStreamOrderedDeliveryWithLossAndRetransmit(t *testing.T) {
	a, b := newPipe()
	dropped := map[int64]bool{}
	var mu sync.Mutex
	// Drop the first transmission of every odd sequence number.
	a.drop = func(d []byte) bool {
		m, err := ssmmessages.Unmarshal(d)
		if err != nil || m.MessageType != ssmmessages.OutputStreamData || m.SequenceNumber%2 == 0 {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if dropped[m.SequenceNumber] {
			return false
		}
		dropped[m.SequenceNumber] = true
		return true
	}
	sender := ssmmessages.NewStream(a, ssmmessages.OutputStreamData)
	receiver := ssmmessages.NewStream(b, ssmmessages.InputStreamData)
	defer sender.Close()
	defer receiver.Close()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		// The sender must process acknowledgements.
		for {
			if _, err := sender.Receive(); err != nil {
				return
			}
		}
	}()
	go func() { _ = sender.RunResender(t.Context()) }()

	for i := range 20 {
		require.NoError(t, sender.Send(ssmmessages.PayloadOutput, []byte{byte(i)}))
	}
	for i := range 20 {
		m, err := receiver.Receive()
		require.NoError(t, err)
		assert.Equal(t, int64(i), m.SequenceNumber)
		assert.Equal(t, []byte{byte(i)}, m.Payload)
	}
	require.Eventually(t, func() bool { return sender.Pending() == 0 }, 5*time.Second, 10*time.Millisecond)
}

func TestStreamDeduplicatesRetransmissions(t *testing.T) {
	a, b := newPipe()
	receiver := ssmmessages.NewStream(b, ssmmessages.InputStreamData)
	frame, _ := ssmmessages.NewStreamMessage(ssmmessages.OutputStreamData, 0, ssmmessages.PayloadOutput, []byte("once")).Marshal()
	second, _ := ssmmessages.NewStreamMessage(ssmmessages.OutputStreamData, 1, ssmmessages.PayloadOutput, []byte("twice")).Marshal()
	require.NoError(t, a.WriteFrame(frame))
	require.NoError(t, a.WriteFrame(frame))
	require.NoError(t, a.WriteFrame(second))
	m, err := receiver.Receive()
	require.NoError(t, err)
	assert.Equal(t, "once", string(m.Payload))
	m, err = receiver.Receive()
	require.NoError(t, err)
	assert.Equal(t, "twice", string(m.Payload))
}
