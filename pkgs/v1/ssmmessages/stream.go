package ssmmessages

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Conn is the minimal frame transport (one binary WebSocket message per frame).
type Conn interface {
	WriteFrame(data []byte) error
	ReadFrame() ([]byte, error)
	Close() error
}

// Stream implements the reliable, ordered stream-data layer on top of Conn:
// outgoing messages get sequence numbers and are retransmitted until
// acknowledged; incoming stream messages are acknowledged, de-duplicated,
// and delivered in order. It is used on both ends of a session (the server
// speaking to the plugin and the agent speaking to the server).
type Stream struct {
	conn     Conn
	outType  string
	resend   time.Duration
	maxTries int

	writeMu sync.Mutex
	mu      sync.Mutex
	nextOut int64
	pending map[int64]*pendingMsg

	expected int64
	buffered map[int64]*Message

	closed chan struct{}
	once   sync.Once
}

type pendingMsg struct {
	frame  []byte
	sentAt time.Time
	tries  int
}

// ErrResendExhausted is returned when the peer stops acknowledging.
var ErrResendExhausted = errors.New("ssmmessages: peer did not acknowledge stream data")

// NewStream wraps conn. outType is the message type used for outgoing stream
// data (OutputStreamData on the agent/server side, InputStreamData on the
// client side).
func NewStream(conn Conn, outType string) *Stream {
	return &Stream{
		conn: conn, outType: outType, resend: 200 * time.Millisecond, maxTries: 1500,
		pending: make(map[int64]*pendingMsg), buffered: make(map[int64]*Message),
		closed: make(chan struct{}),
	}
}

// Send queues a stream data message with the next sequence number.
func (s *Stream) Send(payloadType uint32, payload []byte) error {
	s.mu.Lock()
	seq := s.nextOut
	s.nextOut++
	frame, err := NewStreamMessage(s.outType, seq, payloadType, payload).Marshal()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.pending[seq] = &pendingMsg{frame: frame, sentAt: time.Now(), tries: 1}
	s.mu.Unlock()
	return s.write(frame)
}

// SendControl sends a non-sequenced message (channel_closed, ...).
func (s *Stream) SendControl(m *Message) error {
	frame, err := m.Marshal()
	if err != nil {
		return err
	}
	return s.write(frame)
}

func (s *Stream) write(frame []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteFrame(frame)
}

// Receive returns the next in-order stream data message (or a control
// message such as channel_closed). Acknowledges are handled internally.
func (s *Stream) Receive() (*Message, error) {
	for {
		s.mu.Lock()
		if m, ok := s.buffered[s.expected]; ok {
			delete(s.buffered, s.expected)
			s.expected++
			s.mu.Unlock()
			return m, nil
		}
		s.mu.Unlock()

		data, err := s.conn.ReadFrame()
		if err != nil {
			return nil, err
		}
		m, err := Unmarshal(data)
		if err != nil {
			continue // corrupted frames are dropped; the peer retransmits
		}
		switch m.MessageType {
		case Acknowledge:
			s.handleAck(m)
		case InputStreamData, OutputStreamData:
			if err := s.SendControl(NewAcknowledge(m)); err != nil {
				return nil, err
			}
			s.mu.Lock()
			switch {
			case m.SequenceNumber == s.expected:
				s.expected++
				s.mu.Unlock()
				return m, nil
			case m.SequenceNumber > s.expected:
				s.buffered[m.SequenceNumber] = m
			}
			s.mu.Unlock()
		case StartPublication, PausePublication:
		default:
			return m, nil
		}
	}
}

func (s *Stream) handleAck(m *Message) {
	var ack AcknowledgeContent
	if err := jsonUnmarshal(m.Payload, &ack); err != nil {
		return
	}
	s.mu.Lock()
	delete(s.pending, ack.SequenceNumber)
	s.mu.Unlock()
}

// RunResender retransmits unacknowledged messages until ctx is done or the
// stream is closed. It returns ErrResendExhausted if the peer is gone.
func (s *Stream) RunResender(ctx context.Context) error {
	t := time.NewTicker(s.resend / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.closed:
			return nil
		case <-t.C:
		}
		var due [][]byte
		s.mu.Lock()
		now := time.Now()
		for _, p := range s.pending {
			if now.Sub(p.sentAt) < s.resend {
				continue
			}
			if p.tries >= s.maxTries {
				s.mu.Unlock()
				return ErrResendExhausted
			}
			p.tries++
			p.sentAt = now
			due = append(due, p.frame)
		}
		s.mu.Unlock()
		for _, f := range due {
			if err := s.write(f); err != nil {
				return err
			}
		}
	}
}

// ResumeFrom continues the sequence state of a previous stream over a new
// connection (peer reconnected with ResumeSession and kept its counters).
// Unacknowledged messages are retransmitted on the new connection.
func (s *Stream) ResumeFrom(old *Stream) error {
	old.mu.Lock()
	nextOut, expected := old.nextOut, old.expected
	pending := make(map[int64]*pendingMsg, len(old.pending))
	for k, v := range old.pending {
		pending[k] = &pendingMsg{frame: v.frame, sentAt: time.Time{}, tries: v.tries}
	}
	old.mu.Unlock()

	s.mu.Lock()
	s.nextOut, s.expected, s.pending = nextOut, expected, pending
	frames := make([][]byte, 0, len(pending))
	for seq := range pending {
		frames = append(frames, pending[seq].frame)
		pending[seq].sentAt = time.Now()
	}
	s.mu.Unlock()
	for _, f := range frames {
		if err := s.write(f); err != nil {
			return err
		}
	}
	return nil
}

// Pending returns the number of unacknowledged outgoing messages.
func (s *Stream) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

// WaitDrained blocks until all sent messages are acknowledged or timeout.
func (s *Stream) WaitDrained(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && s.Pending() > 0 {
		time.Sleep(20 * time.Millisecond)
	}
}

// Close closes the underlying connection.
func (s *Stream) Close() error {
	var err error
	s.once.Do(func() {
		close(s.closed)
		err = s.conn.Close()
	})
	return err
}
