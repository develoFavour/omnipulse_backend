package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
)

const aiReplySubject = "ai.reply.requested"

type AIReplyJob struct {
	TenantID       string `json:"tenant_id"`
	ConversationID string `json:"conversation_id"`
	Channel        string `json:"channel"`
	Recipient      string `json:"recipient"`
	IncomingText   string `json:"incoming_text"`
	PlatformID     string `json:"platform_id,omitempty"`
}

type AIReplyDispatcher interface {
	EnqueueAIReply(ctx context.Context, job AIReplyJob) error
}

type NatsAIReplyDispatcher struct {
	js nats.JetStreamContext
}

func NewNatsAIReplyDispatcher(js nats.JetStreamContext) *NatsAIReplyDispatcher {
	return &NatsAIReplyDispatcher{js: js}
}

func (d *NatsAIReplyDispatcher) EnqueueAIReply(ctx context.Context, job AIReplyJob) error {
	if d == nil || d.js == nil {
		return fmt.Errorf("AI reply queue is unavailable")
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to encode AI reply job: %w", err)
	}
	if _, err := d.js.Publish(aiReplySubject, payload, nats.Context(ctx)); err != nil {
		return fmt.Errorf("failed to enqueue AI reply: %w", err)
	}
	return nil
}

type AIReplyProcessor func(context.Context, AIReplyJob) error

type AIReplyWorker struct {
	js        nats.JetStreamContext
	processor AIReplyProcessor
	sub       *nats.Subscription
}

func NewAIReplyWorker(js nats.JetStreamContext, processor AIReplyProcessor) *AIReplyWorker {
	return &AIReplyWorker{js: js, processor: processor}
}

func (w *AIReplyWorker) Start() error {
	if w == nil || w.js == nil || w.processor == nil {
		return fmt.Errorf("AI reply worker is not configured")
	}
	sub, err := w.js.QueueSubscribe(aiReplySubject, "ai-reply-workers", func(msg *nats.Msg) {
		var job AIReplyJob
		if err := json.Unmarshal(msg.Data, &job); err != nil {
			log.Printf("[AI-QUEUE] Invalid AI reply job: %v", err)
			_ = msg.Term()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := w.processor(ctx, job)
		cancel()
		if err != nil {
			log.Printf("[AI-QUEUE] Reply job failed for tenant %s: %v", job.TenantID, err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	}, nats.ManualAck(), nats.AckWait(60*time.Second), nats.MaxDeliver(5))
	if err != nil {
		return fmt.Errorf("failed to subscribe to AI reply queue: %w", err)
	}
	w.sub = sub
	return nil
}

func (w *AIReplyWorker) Stop() error {
	if w == nil || w.sub == nil {
		return nil
	}
	return w.sub.Unsubscribe()
}
