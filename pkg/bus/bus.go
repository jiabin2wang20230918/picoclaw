package bus

import (
	"context"
	"sync"
)

type MessageBus struct {
	inbound  chan InboundMessage
	outbound chan OutboundMessage
	steering chan SteeringMessage // steering messages for interruption
	handlers map[string]MessageHandler
	closed   bool
	mu       sync.RWMutex
}

func NewMessageBus() *MessageBus {
	return &MessageBus{
		inbound:  make(chan InboundMessage, 100),
		outbound: make(chan OutboundMessage, 100),
		steering: make(chan SteeringMessage, 10), // smaller buffer for immediate signals
		handlers: make(map[string]MessageHandler),
	}
}

func (mb *MessageBus) PublishInbound(msg InboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	mb.inbound <- msg
}

func (mb *MessageBus) ConsumeInbound(ctx context.Context) (InboundMessage, bool) {
	select {
	case msg := <-mb.inbound:
		return msg, true
	case <-ctx.Done():
		return InboundMessage{}, false
	}
}

func (mb *MessageBus) PublishOutbound(msg OutboundMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	mb.outbound <- msg
}

func (mb *MessageBus) SubscribeOutbound(ctx context.Context) (OutboundMessage, bool) {
	select {
	case msg := <-mb.outbound:
		return msg, true
	case <-ctx.Done():
		return OutboundMessage{}, false
	}
}

func (mb *MessageBus) RegisterHandler(channel string, handler MessageHandler) {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	mb.handlers[channel] = handler
}

func (mb *MessageBus) GetHandler(channel string) (MessageHandler, bool) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	handler, ok := mb.handlers[channel]
	return handler, ok
}

func (mb *MessageBus) Close() {
	mb.mu.Lock()
	defer mb.mu.Unlock()
	if mb.closed {
		return
	}
	mb.closed = true
	close(mb.inbound)
	close(mb.outbound)
	close(mb.steering)
}

// PublishSteering publishes a steering message (non-blocking).
// If the channel is full, it drops the oldest message and inserts the new one.
func (mb *MessageBus) PublishSteering(msg SteeringMessage) {
	mb.mu.RLock()
	defer mb.mu.RUnlock()
	if mb.closed {
		return
	}
	select {
	case mb.steering <- msg:
	default:
		// channel full, drop oldest and insert new
		select {
		case <-mb.steering:
			mb.steering <- msg
		default:
		}
	}
}

// ConsumeSteering returns a steering message if available (non-blocking).
func (mb *MessageBus) ConsumeSteering() (SteeringMessage, bool) {
	select {
	case msg := <-mb.steering:
		return msg, true
	default:
		return SteeringMessage{}, false
	}
}

// ConsumeSteeringForSession returns a steering message matching the session.
// Non-blocking: drains messages until a match is found or channel is empty.
// Non-matching messages are dropped (simplified for single-session use).
func (mb *MessageBus) ConsumeSteeringForSession(sessionKey string) (SteeringMessage, bool) {
	for {
		select {
		case msg := <-mb.steering:
			if msg.SessionKey == sessionKey {
				return msg, true
			}
			// Not for this session, continue draining
		default:
			return SteeringMessage{}, false
		}
	}
}
