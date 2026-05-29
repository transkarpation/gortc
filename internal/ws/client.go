package ws

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 4096
)

var newline = []byte{'\n'}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CheckOrigin allows all origins. Tighten this for production use.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	// Buffered channel of outbound messages.
	send chan []byte
	// channels is the set of channels this client is subscribed to. It is only
	// ever accessed from the hub's Run goroutine.
	channels map[string]bool
	// appID and userID identify the authenticated principal behind the socket.
	appID  string
	userID string
}

// readPump drains the connection so that control frames (pong, close) are
// processed and a dropped peer is detected. Clients are receive-only: any data
// frames they send are ignored. Publishing happens via the HTTP /publish
// endpoint, not over the socket.
//
// It runs in a per-connection goroutine, ensuring at most one reader.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("ws: unexpected close: %v", err)
			}
			break
		}
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. It ensures
// that there is at most one writer to a connection by executing all writes
// from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message.
			n := len(c.send)
			for range n {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ServeWS handles websocket requests from the peer, upgrading the HTTP
// connection and registering a new Client with the hub.
//
// The handshake is authenticated first (see Authenticator); on failure it is
// rejected with 401 before the connection is upgraded.
func ServeWS(hub *Hub, auth Authenticator, w http.ResponseWriter, r *http.Request) {
	principal, err := auth.authenticate(r)
	if err != nil {
		log.Printf("ws: handshake rejected: %v", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws: upgrade failed: %v", err)
		return
	}
	client := &Client{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		channels: make(map[string]bool),
		appID:    principal.AppID,
		userID:   principal.UserID,
	}
	client.hub.register <- client
	log.Printf("ws: connected app=%s user=%s", client.appID, client.userID)

	// Subscribe to the channels named in the "channels" query parameter
	// (comma-separated), e.g. /ws?...&channels=news,sports.
	for ch := range strings.SplitSeq(r.URL.Query().Get("channels"), ",") {
		if ch = strings.TrimSpace(ch); ch != "" {
			client.hub.subscribe <- subscription{client: client, channel: ch}
		}
	}

	// Allow collection of memory referenced by the caller by doing all work
	// in new goroutines.
	go client.writePump()
	go client.readPump()
}
