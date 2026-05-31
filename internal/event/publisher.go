// Package event publishes IPAM domain events to Redis pub/sub so other
// services (e.g. the DNS module) can react. It mirrors the publisher
// pattern used by go-tangra-lcm's internal/event package.
package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
)

// Publisher publishes IPAM events to Redis.
type Publisher struct {
	log         *log.Helper
	redisClient *redis.Client
}

// NewPublisher creates an event publisher. If the Redis client is nil
// (Redis not configured), Publish becomes a no-op.
func NewPublisher(ctx *bootstrap.Context, redisClient *redis.Client) *Publisher {
	return &Publisher{
		log:         ctx.NewLoggerHelper("event/publisher/ipam-service"),
		redisClient: redisClient,
	}
}

// Publish wraps data in the standard envelope and publishes it to
// the "<TopicPrefix>.<topic>" Redis channel. Errors are logged, not
// returned to the caller — event publishing must never fail the
// originating operation.
func (p *Publisher) Publish(ctx context.Context, topic string, tenantID uint32, data interface{}) {
	if p.redisClient == nil {
		return
	}

	env := Envelope{
		ID:        uuid.New().String(),
		Type:      topic,
		Source:    EventSource,
		Timestamp: time.Now().UTC(),
		TenantID:  tenantID,
		Data:      data,
	}

	payload, err := json.Marshal(env)
	if err != nil {
		p.log.Errorf("failed to marshal %s event: %v", topic, err)
		return
	}

	channel := TopicPrefix + "." + topic
	if err := p.redisClient.Publish(ctx, channel, payload).Err(); err != nil {
		p.log.Errorf("failed to publish event to %s: %v", channel, err)
		return
	}
	p.log.Infof("published event to %s: %s", channel, env.ID)
}

// PublishIPAddressCreated publishes an ip_address.created event.
func (p *Publisher) PublishIPAddressCreated(ctx context.Context, e *IPAddressEvent) {
	p.Publish(ctx, TopicIPAddressCreated, e.TenantID, e)
}

// PublishIPAddressDeleted publishes an ip_address.deleted event.
func (p *Publisher) PublishIPAddressDeleted(ctx context.Context, e *IPAddressEvent) {
	p.Publish(ctx, TopicIPAddressDeleted, e.TenantID, e)
}
