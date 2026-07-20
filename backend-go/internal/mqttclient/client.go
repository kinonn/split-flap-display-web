// Package mqttclient wraps the paho MQTT client to provide a lifecycle
// managed connection together with a pub/sub queue system for
// display-state and raw-history events.
package mqttclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"splitflap-web/internal/models"
	"splitflap-web/internal/queue"
)

// DisplayStateMsg is the JSON-shaped message stored inside a
// display-state event's "message" field.
type DisplayStateMsg struct {
	Message string `json:"message"`
}

// Client is a long-running MQTT connection that pushes incoming state
// messages to subscribed queues.
type Client struct {
	host   string
	port   int
	clientID string
	subscribeTopic string

	client pahomqtt.Client
	opts   *pahomqtt.ClientOptions

	connected atomic.Bool

	mu        sync.Mutex
	latest    *string          // latest payload received on subscribe topic
	history   []models.MQTTHistoryEntry // ring buffer, cap 500

	displaySubsMu sync.Mutex
	displaySubs  map[*queue.Queue]struct{}

	historySubsMu sync.Mutex
	historySubs   map[*queue.Queue]struct{}

	cancel context.CancelFunc
	done   chan struct{}
}

// New constructs an unconnected Client.
func New(host string, port int, clientID, subscribeTopic string) *Client {
	c := &Client{
		host:           host,
		port:           port,
		clientID:       clientID,
		subscribeTopic: subscribeTopic,
		history:        make([]models.MQTTHistoryEntry, 0, 500),
		displaySubs:    make(map[*queue.Queue]struct{}),
		historySubs:    make(map[*queue.Queue]struct{}),
		done:           make(chan struct{}),
	}
	opts := pahomqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s:%d", host, port))
	opts.SetClientID(clientID)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(func(_ pahomqtt.Client) {
		c.connected.Store(true)
		log.Printf("mqtt: connected to broker %s:%d", host, port)
		if token := c.client.Subscribe(subscribeTopic, 0, c.handleMessage); token.Wait() && token.Error() != nil {
			log.Printf("mqtt: subscribe to %q failed: %v", subscribeTopic, token.Error())
		} else {
			log.Printf("mqtt: subscribed to %q", subscribeTopic)
		}
	})
	opts.SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
		c.connected.Store(false)
		log.Printf("mqtt: connection lost: %v", err)
	})
	c.opts = opts
	return c
}

// Start spawns the background connection goroutine. It is safe to call
// multiple times; only the first call has any effect.
func (c *Client) Start(ctx context.Context) {
	c.client = pahomqtt.NewClient(c.opts)
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	go func() {
		defer close(c.done)
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			token := c.client.Connect()
			if token.Wait() && token.Error() != nil {
				if first {
					log.Printf("mqtt: initial connect failed: %v", token.Error())
				} else {
					log.Printf("mqtt: connect failed: %v", token.Error())
				}
				first = false
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
				continue
			}
			// paho runs its own reconnect loop; wait for cancellation.
			<-ctx.Done()
			return
		}
	}()
}

// Stop disconnects cleanly and waits for the background goroutine.
func (c *Client) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.client != nil {
		c.client.Disconnect(250)
	}
	<-c.done
}

// Publish publishes a raw UTF-8 string payload to the given topic.
func (c *Client) Publish(topic, payload string, qos byte) error {
	if !c.connected.Load() {
		return fmt.Errorf("mqtt client not connected")
	}
	token := c.client.Publish(topic, qos, false, payload)
	// Fire-and-retain: wait briefly for the token to complete to surface
	// errors, but paho may queue if disconnected.
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt publish timeout")
	}
	return token.Error()
}

// Connected reports whether the broker connection is currently active.
func (c *Client) Connected() bool { return c.connected.Load() }

// GetLatestMessage returns the latest payload received on the subscribe
// topic and true, or "" and false if none has been received.
func (c *Client) GetLatestMessage() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		return "", false
	}
	return *c.latest, true
}

// GetHistory returns a copy of the raw MQTT message history.
func (c *Client) GetHistory() []models.MQTTHistoryEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]models.MQTTHistoryEntry, len(c.history))
	copy(out, c.history)
	return out
}

// SubscribeDisplayState registers a queue to receive display-state
// events. If a latest payload has already been received, the queue is
// seeded with a single event.
func (c *Client) SubscribeDisplayState() *queue.Queue {
	q := queue.New(100)
	c.mu.Lock()
	latest := c.latest
	c.mu.Unlock()
	if latest != nil {
		data, _ := json.Marshal(struct {
			Type    string          `json:"type"`
			Message DisplayStateMsg `json:"message"`
		}{Type: "display-state", Message: DisplayStateMsg{Message: *latest}})
		q.Push(queue.Event{Name: "display-state", Data: data})
	}
	c.displaySubsMu.Lock()
	c.displaySubs[q] = struct{}{}
	c.displaySubsMu.Unlock()
	return q
}

// UnsubscribeDisplayState removes a queue from the display-state
// subscriber set.
func (c *Client) UnsubscribeDisplayState(q *queue.Queue) {
	c.displaySubsMu.Lock()
	delete(c.displaySubs, q)
	c.displaySubsMu.Unlock()
}

// SubscribeQueue registers a queue for the raw MQTT history pub/sub.
// The queue is seeded with up to 500 existing history entries.
func (c *Client) SubscribeQueue() *queue.Queue {
	q := queue.New(500)
	c.mu.Lock()
	snapshot := append([]models.MQTTHistoryEntry(nil), c.history...)
	c.mu.Unlock()
	for _, h := range snapshot {
		data, _ := json.Marshal(h)
		q.Push(queue.Event{Name: "history", Data: data})
	}
	c.historySubsMu.Lock()
	c.historySubs[q] = struct{}{}
	c.historySubsMu.Unlock()
	return q
}

// UnsubscribeQueue removes a queue from the raw MQTT history pub/sub.
func (c *Client) UnsubscribeQueue(q *queue.Queue) {
	c.historySubsMu.Lock()
	delete(c.historySubs, q)
	c.historySubsMu.Unlock()
}

// handleMessage is invoked by paho for each received MQTT message on the
// configured subscribe topic.
func (c *Client) handleMessage(_ pahomqtt.Client, msg pahomqtt.Message) {
	payload := string(msg.Payload())
	topic := msg.Topic()

	c.mu.Lock()
	c.latest = &payload
	if len(c.history) >= 500 {
		c.history = c.history[1:]
	}
	c.history = append(c.history, models.MQTTHistoryEntry{Topic: topic, Payload: payload})
	c.mu.Unlock()

	rawData, _ := json.Marshal(models.MQTTHistoryEntry{Topic: topic, Payload: payload})
	dsData, _ := json.Marshal(struct {
		Type    string          `json:"type"`
		Message DisplayStateMsg `json:"message"`
	}{Type: "display-state", Message: DisplayStateMsg{Message: payload}})

	// Notify display-state subscribers.
	c.displaySubsMu.Lock()
	subs := make([]*queue.Queue, 0, len(c.displaySubs))
	for q := range c.displaySubs {
		subs = append(subs, q)
	}
	c.displaySubsMu.Unlock()
	for _, q := range subs {
		q.Push(queue.Event{Name: "display-state", Data: dsData})
	}

	// Notify raw history subscribers.
	c.historySubsMu.Lock()
	hsubs := make([]*queue.Queue, 0, len(c.historySubs))
	for q := range c.historySubs {
		hsubs = append(hsubs, q)
	}
	c.historySubsMu.Unlock()
	for _, q := range hsubs {
		q.Push(queue.Event{Name: "history", Data: rawData})
	}
}