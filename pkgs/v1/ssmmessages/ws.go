package ssmmessages

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// writeTimeout bounds a single data write so a peer that stops reading
// cannot block writers (and everything waiting on them) forever.
const writeTimeout = 30 * time.Second

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// WSConn adapts a gorilla WebSocket connection to Conn. Text frames are
// skipped by ReadFrame (only the initial open_data_channel frame is text and
// is read explicitly with ReadText).
type WSConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
}

// NewWSConn wraps ws and enables keepalive pings.
func NewWSConn(ws *websocket.Conn) *WSConn {
	c := &WSConn{ws: ws}
	// WriteControl is safe to call concurrently with WriteMessage, so pongs
	// and Close never wait behind a data write blocked on a stalled peer.
	ws.SetPingHandler(func(data string) error {
		return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	return c
}

// WriteFrame sends one binary message.
func (c *WSConn) WriteFrame(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.ws.WriteMessage(websocket.BinaryMessage, data)
}

// WriteText sends one text message.
func (c *WSConn) WriteText(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.ws.WriteMessage(websocket.TextMessage, data)
}

// ReadFrame returns the next binary message.
func (c *WSConn) ReadFrame() ([]byte, error) {
	for {
		mt, data, err := c.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt == websocket.BinaryMessage {
			return data, nil
		}
	}
}

// ReadText returns the next text message (used for open_data_channel).
func (c *WSConn) ReadText(timeout time.Duration) ([]byte, error) {
	_ = c.ws.SetReadDeadline(time.Now().Add(timeout))
	defer c.ws.SetReadDeadline(time.Time{})
	for {
		mt, data, err := c.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt == websocket.TextMessage {
			return data, nil
		}
	}
}

// Close closes the connection after a best-effort close frame.
func (c *WSConn) Close() error {
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return c.ws.Close()
}
