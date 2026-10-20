package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"omnipulse/apps/api-gateway/internal/domain"
)

const knowledgeIngestionSubject = "ai.knowledge.ingestion.requested"

type KnowledgeIngestionDispatcher interface {
	EnqueueKnowledgeIngestion(ctx context.Context, jobID string) error
}

type NatsKnowledgeIngestionDispatcher struct {
	js nats.JetStreamContext
}

func NewNatsKnowledgeIngestionDispatcher(js nats.JetStreamContext) *NatsKnowledgeIngestionDispatcher {
	return &NatsKnowledgeIngestionDispatcher{js: js}
}

func (d *NatsKnowledgeIngestionDispatcher) EnqueueKnowledgeIngestion(ctx context.Context, jobID string) error {
	if d == nil || d.js == nil {
		return fmt.Errorf("knowledge ingestion queue is unavailable")
	}
	payload, err := json.Marshal(map[string]string{"job_id": jobID})
	if err != nil {
		return fmt.Errorf("failed to encode knowledge ingestion job: %w", err)
	}
	if _, err := d.js.Publish(knowledgeIngestionSubject, payload, nats.Context(ctx)); err != nil {
		return fmt.Errorf("failed to enqueue knowledge ingestion job: %w", err)
	}
	return nil
}

type KnowledgeIngestionWorker struct {
	js       nats.JetStreamContext
	repo     domain.AIAgentRepository
	aiEngine *AIEngineService
	sub      *nats.Subscription
}

func NewKnowledgeIngestionWorker(js nats.JetStreamContext, repo domain.AIAgentRepository, aiEngine *AIEngineService) *KnowledgeIngestionWorker {
	return &KnowledgeIngestionWorker{js: js, repo: repo, aiEngine: aiEngine}
}

func (w *KnowledgeIngestionWorker) Start() error {
	if w == nil || w.js == nil || w.repo == nil || w.aiEngine == nil {
		return fmt.Errorf("knowledge ingestion worker is not configured")
	}
	sub, err := w.js.QueueSubscribe(knowledgeIngestionSubject, "knowledge-ingestion-workers", func(msg *nats.Msg) {
		var payload struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(msg.Data, &payload); err != nil || payload.JobID == "" {
			log.Printf("[KNOWLEDGE-QUEUE] Invalid ingestion job payload: %v", err)
			_ = msg.Term()
			return
		}
		log.Printf("[KNOWLEDGE-QUEUE] Received ingestion job %s", payload.JobID)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		err := w.process(ctx, payload.JobID)
		cancel()
		if err != nil {
			log.Printf("[KNOWLEDGE-QUEUE] Ingestion job %s failed: %v", payload.JobID, err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	}, nats.ManualAck(), nats.AckWait(6*time.Minute), nats.MaxDeliver(5))
	if err != nil {
		return fmt.Errorf("failed to subscribe to knowledge ingestion queue: %w", err)
	}
	w.sub = sub
	log.Printf("[KNOWLEDGE-QUEUE] Worker subscribed to %s (queue=knowledge-ingestion-workers)", knowledgeIngestionSubject)
	return nil
}

func (w *KnowledgeIngestionWorker) process(ctx context.Context, jobID string) error {
	job, err := w.repo.ClaimKnowledgeIngestionJob(ctx, jobID)
	if err != nil {
		return err
	}
	if job == nil {
		// Already completed, failed, or currently being processed by another worker.
		log.Printf("[KNOWLEDGE-QUEUE] Job %s was not claimable (already complete, failed, or still within its processing lease)", jobID)
		return nil
	}
	log.Printf("[KNOWLEDGE-QUEUE] Claimed job %s (filename=%q, content_type=%q, source_bytes=%d)", job.ID, job.Filename, job.ContentType, len(job.SourceData))

	fail := func(message string) error {
		failCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.repo.FailKnowledgeIngestionJob(failCtx, jobID, message); err != nil {
			return err
		}
		return nil
	}

	text, err := ExtractDocumentText(ctx, job.Filename, job.ContentType, job.SourceData)
	if err != nil {
		log.Printf("[KNOWLEDGE-QUEUE] Text extraction failed for job %s: %v", jobID, err)
		if failErr := fail(err.Error()); failErr != nil {
			return failErr
		}
		return nil
	}
	if text == "" {
		log.Printf("[KNOWLEDGE-QUEUE] Text extraction returned no readable text for job %s", jobID)
		if failErr := fail("No readable text was found in the document"); failErr != nil {
			return failErr
		}
		return nil
	}

	items, err := w.aiEngine.ExtractKnowledgeItemsFromDocument(ctx, job.Filename, text)
	if err != nil {
		log.Printf("[KNOWLEDGE-QUEUE] AI knowledge extraction failed for job %s: %v", jobID, err)
		if failErr := fail("The document could not be analyzed right now. Please try again."); failErr != nil {
			return failErr
		}
		return nil
	}
	if len(items) == 0 {
		log.Printf("[KNOWLEDGE-QUEUE] AI returned zero knowledge items for job %s", jobID)
		if failErr := fail("No discrete facts or Q&A items could be identified from the provided content"); failErr != nil {
			return failErr
		}
		return nil
	}

	if err := w.repo.CompleteKnowledgeIngestionJob(ctx, jobID, items); err != nil {
		return err
	}
	log.Printf("[KNOWLEDGE-QUEUE] Completed ingestion job %s with %d knowledge items", jobID, len(items))
	return nil
}

func (w *KnowledgeIngestionWorker) Stop() error {
	if w == nil || w.sub == nil {
		return nil
	}
	return w.sub.Unsubscribe()
}
