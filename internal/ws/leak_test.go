package ws

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestDisconnectCleansUpHub verifies that connecting and then disconnecting many
// clients leaves no entries behind in the hub's clients, connection-id, and
// channel maps, and leaks no goroutines.
func TestDisconnectCleansUpHub(t *testing.T) {
	const apiKey, appID, userBase = "k", "app", "user"
	auth := NewAuthenticator(map[string]App{apiKey: {AppID: appID, APISecret: []byte("secret")}})

	hub := NewHub()
	go hub.Run()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWS(hub, auth, w, r)
	}))
	defer srv.Close()

	if got := hub.Stats(); got != (Stats{}) {
		t.Fatalf("hub not empty before test: %+v", got)
	}

	goroutinesBefore := runtime.NumGoroutine()

	const n = 50
	conns := make([]*websocket.Conn, 0, n)
	for i := range n {
		userID := userBase + strconv.Itoa(i)
		token, err := auth.Mint(apiKey, userID, time.Hour)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		q := url.Values{"apiKey": {apiKey}, "userId": {userID}, "authorization": {token}}
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?" + q.Encode()

		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		// Read the welcome message so the connection is fully established.
		if _, _, err := c.ReadMessage(); err != nil {
			t.Fatalf("read welcome %d: %v", i, err)
		}
		conns = append(conns, c)
	}

	// Every client is also subscribed to a shared channel via its connection id,
	// so we can confirm shared channels are torn down too.
	waitForStats(t, hub, Stats{Clients: n, Connections: n, Channels: n})

	// Now disconnect everyone.
	for _, c := range conns {
		c.Close()
	}

	// The hub must drain back to empty.
	waitForStats(t, hub, Stats{})

	// And no goroutines should have leaked (allow a small slack for the runtime).
	if leaked := runtime.NumGoroutine() - goroutinesBefore; leaked > 2 {
		t.Errorf("possible goroutine leak: %d extra goroutines", leaked)
	}
}

// waitForStats polls hub.Stats until it equals want or the deadline passes.
func waitForStats(t *testing.T, hub *Hub, want Stats) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got Stats
	for time.Now().Before(deadline) {
		if got = hub.Stats(); got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hub stats never reached %+v; last seen %+v", want, got)
}
