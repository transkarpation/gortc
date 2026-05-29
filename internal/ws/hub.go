package ws

import (
	"encoding/json"
	"log/slog"
)

// Hub maintains the set of active clients and routes published messages to the
// clients subscribed to each channel. All of its state is owned by the Run
// goroutine; interact with it only through its channels and exported methods.
type Hub struct {
	register      chan *Client
	unregister    chan *Client
	subscribe     chan subscription
	subscribeConn chan subscribeRequest
	publishes     chan publication

	// clients holds every connected client.
	clients map[*Client]bool
	// byConnID indexes connected clients by their connection id.
	byConnID map[string]*Client
	// channels maps a channel name to the set of clients subscribed to it.
	channels map[string]map[*Client]bool
}

// subscription is a request to (un)subscribe a client to a channel.
type subscription struct {
	client  *Client
	channel string
}

// subscribeRequest subscribes the client identified by connID to channels. The
// hub reports back over reply whether a matching connection was found.
type subscribeRequest struct {
	connID   string
	channels []string
	reply    chan bool
}

// publication is a message to be delivered to a channel's subscribers.
type publication struct {
	channel string
	data    []byte
}

// outbound is the JSON envelope delivered to subscribed clients.
type outbound struct {
	Channel string `json:"channel"`
	Data    string `json:"data"`
}

// NewHub creates a Hub ready to be run.
func NewHub() *Hub {
	return &Hub{
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		subscribe:     make(chan subscription),
		subscribeConn: make(chan subscribeRequest),
		publishes:     make(chan publication),
		clients:       make(map[*Client]bool),
		byConnID:      make(map[string]*Client),
		channels:      make(map[string]map[*Client]bool),
	}
}

// Run processes client lifecycle and pub/sub events. It blocks, so it is meant
// to be launched in its own goroutine.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			h.byConnID[client.connID] = client
		case client := <-h.unregister:
			h.removeClient(client)
		case s := <-h.subscribe:
			h.addSubscription(s)
		case req := <-h.subscribeConn:
			h.subscribeByConn(req)
		case p := <-h.publishes:
			h.deliver(p)
		}
	}
}

// Publish delivers data to every client subscribed to channel. It is safe to
// call from any goroutine.
func (h *Hub) Publish(channel string, data []byte) {
	h.publishes <- publication{channel: channel, data: data}
}

// Subscribe subscribes the connection identified by connID to channels and
// reports whether a matching connection was found. Safe for concurrent use.
func (h *Hub) Subscribe(connID string, channels []string) bool {
	reply := make(chan bool, 1)
	h.subscribeConn <- subscribeRequest{connID: connID, channels: channels, reply: reply}
	return <-reply
}

func (h *Hub) subscribeByConn(req subscribeRequest) {
	client, ok := h.byConnID[req.connID]
	if ok {
		for _, channel := range req.channels {
			if channel != "" {
				h.addSubscription(subscription{client: client, channel: channel})
			}
		}
	}
	req.reply <- ok
}

func (h *Hub) addSubscription(s subscription) {
	subs := h.channels[s.channel]
	if subs == nil {
		subs = make(map[*Client]bool)
		h.channels[s.channel] = subs
	}
	subs[s.client] = true
	s.client.channels[s.channel] = true
}

func (h *Hub) deliver(p publication) {
	subs := h.channels[p.channel]
	if len(subs) == 0 {
		return
	}
	payload, err := json.Marshal(outbound{Channel: p.channel, Data: string(p.data)})
	if err != nil {
		slog.Error("ws marshal publication", "channel", p.channel, "err", err)
		return
	}
	for client := range subs {
		select {
		case client.send <- payload:
		default:
			// The client's send buffer is full; assume it is dead or stuck and
			// drop it. Safe to mutate subs here: deleting during range is allowed.
			h.removeClient(client)
		}
	}
}

// removeClient unregisters a client, drops all of its subscriptions, and closes
// its send channel.
func (h *Hub) removeClient(client *Client) {
	if _, ok := h.clients[client]; !ok {
		return
	}
	delete(h.clients, client)
	delete(h.byConnID, client.connID)
	for channel := range client.channels {
		h.unsubscribeClient(client, channel)
	}
	close(client.send)
}

// unsubscribeClient removes client from channel and discards the channel once
// it has no subscribers left.
func (h *Hub) unsubscribeClient(client *Client, channel string) {
	if subs, ok := h.channels[channel]; ok {
		delete(subs, client)
		if len(subs) == 0 {
			delete(h.channels, channel)
		}
	}
	delete(client.channels, channel)
}
