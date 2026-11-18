package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Usir233/GENYEE/internal/im"
	"github.com/Usir233/GENYEE/internal/logger"
	secutils "github.com/Usir233/GENYEE/internal/utils"
	ws "github.com/gorilla/websocket"
)

// MessageHandler handles incoming QQ Bot gateway messages.
type MessageHandler func(ctx context.Context, msg *im.IncomingMessage) error

// LongConnClient manages a QQ Bot WebSocket gateway connection and reconnect loop.
type LongConnClient struct {
	client  *Client
	handler MessageHandler

	mu      sync.Mutex
	writeMu sync.Mutex
	conn    *ws.Conn
	seq     *int64
	closed  bool
}

// NewLongConnClient creates a QQ Bot WebSocket gateway client.
func NewLongConnClient(client *Client, handler MessageHandler) *LongConnClient {
	return &LongConnClient{client: client, handler: handler}
}

// Start connects to the QQ Bot WebSocket gateway and automatically reconnects on disconnect.
func (c *LongConnClient) Start(ctx context.Context) error {
	logger.Infof(ctx, "[IM] QQBot WebSocket connecting...")
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := c.connectAndRun(ctx); err != nil {
			if ctx.Err() != nil || c.isClosed() {
				return ctx.Err()
			}
			attempt++
			delay := reconnectDelay(attempt)
			logger.Warnf(ctx, "[QQBot] connection lost: %v, reconnecting in %v", err, delay)
			backoffTimer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				backoffTimer.Stop()
				return ctx.Err()
			case <-backoffTimer.C:
			}
			continue
		}
		attempt = 0
	}
}

// Stop closes the active WebSocket connection and prevents further reconnects.
func (c *LongConnClient) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *LongConnClient) connectAndRun(ctx context.Context) error {
	gatewayURL, err := c.client.GatewayURL(ctx)
	if err != nil {
		return err
	}
	dialer := *ws.DefaultDialer
	dialer.NetDialContext = secutils.SSRFSafeDialContext
	conn, resp, err := dialer.DialContext(ctx, gatewayURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("qqbot client stopped")
	}
	c.conn = conn
	c.mu.Unlock()

	conn.SetReadLimit(1 << 20)
	connCtx, connCancel := context.WithCancel(ctx)
	var heartbeatCancel context.CancelFunc
	defer func() {
		if heartbeatCancel != nil {
			heartbeatCancel()
		}
		connCancel()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var payload gatewayPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			logger.Warnf(ctx, "[QQBot] invalid payload: %v", err)
			continue
		}
		if payload.S != nil {
			seqVal := *payload.S
			c.mu.Lock()
			c.seq = &seqVal
			c.mu.Unlock()
		}
		switch payload.Op {
		case opHello:
			if heartbeatCancel != nil {
				heartbeatCancel()
			}
			hbCtx, hbCancel := context.WithCancel(connCtx)
			heartbeatCancel = hbCancel
			if err := c.handleHello(hbCtx, conn, payload.D); err != nil {
				hbCancel()
				return err
			}
		case opDispatch:
			msg, err := parseGatewayPayload(&payload)
			if err != nil {
				logger.Warnf(ctx, "[QQBot] parse event failed: %v", err)
				continue
			}
			if msg != nil {
				if err := c.handler(ctx, msg); err != nil {
					logger.Errorf(ctx, "[QQBot] handle message failed: %v", err)
				}
			}
		case opReconnect, opInvalidSession:
			return fmt.Errorf("gateway requested reconnect op=%d", payload.Op)
		case opHeartbeatACK:
		}
	}
}

func (c *LongConnClient) writeJSON(conn *ws.Conn, v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return conn.WriteJSON(v)
}

func (c *LongConnClient) handleHello(ctx context.Context, conn *ws.Conn, raw json.RawMessage) error {
	var hello helloData
	if err := json.Unmarshal(raw, &hello); err != nil {
		return err
	}
	if hello.HeartbeatInterval <= 0 {
		hello.HeartbeatInterval = 45000
	}
	token, err := c.client.AccessToken(ctx)
	if err != nil {
		return err
	}
	identify := identifyData{
		Token:   "QQBot " + token,
		Intents: intentGroupAndC2C,
		Shard:   []int{0, 1},
	}
	payloadBytes, err := json.Marshal(identify)
	if err != nil {
		return err
	}
	if err := c.writeJSON(conn, gatewayPayload{Op: opIdentify, D: payloadBytes}); err != nil {
		return err
	}
	go c.heartbeatLoop(ctx, conn, time.Duration(hello.HeartbeatInterval)*time.Millisecond)
	return nil
}

func (c *LongConnClient) heartbeatLoop(ctx context.Context, conn *ws.Conn, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			heartbeat, err := c.heartbeatPayload()
			if err == nil {
				err = c.writeJSON(conn, heartbeat)
			}
			if err != nil {
				return
			}
		}
	}
}

func (c *LongConnClient) heartbeatPayload() (gatewayPayload, error) {
	c.mu.Lock()
	var seq *int64
	if c.seq != nil {
		v := *c.seq
		seq = &v
	}
	c.mu.Unlock()
	data, err := json.Marshal(seq)
	if err != nil {
		return gatewayPayload{}, err
	}
	return gatewayPayload{Op: opHeartbeat, D: data}, nil
}

func (c *LongConnClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func reconnectDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return time.Second
	}
	delay := time.Duration(attempt) * time.Second
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}
