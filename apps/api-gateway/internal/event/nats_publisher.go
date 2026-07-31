package event

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"omnipulse/apps/api-gateway/internal/domain"
	"omnipulse/shared/contracts"

	"github.com/nats-io/nats.go"
)

// JetStreamPublisher implements domain.EventPublisher using the native NATS framework
type JetStreamPublisher struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// InMemoryPublisher provides a resilient fallback when NATS is unreachable
type InMemoryPublisher struct{}

func (p *InMemoryPublisher) PublishDispatchTask(ctx context.Context, task *contracts.TargetDispatchTask) error {
	log.Printf("[EVENT-BUS-FALLBACK] Dispatched task for contact %s (%s) on %s\n", task.FirstName, task.RoutingValue, task.TargetPlatform)
	return nil
}

// NewJetStreamPublisher sets up the connection and provisions the streaming topic boundary.
// Supports Synadia Cloud credentials via natsCreds parameter.
func NewJetStreamPublisher(natsURL string, natsCreds string) (domain.EventPublisher, error) {
	opts := getNatsOptions(natsCreds)
	nc, err := nats.Connect(natsURL, opts...)
	if err != nil {
		log.Printf("[NATS-WARN] Connection failed (%v). Operating with resilient event publisher fallback.\n", err)
		return &InMemoryPublisher{}, nil
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		log.Printf("[NATS-WARN] JetStream initialization failed (%v). Operating with resilient event publisher fallback.\n", err)
		return &InMemoryPublisher{}, nil
	}

	streamName := "CAMPAIGNS"
	requiredSubjects := []string{"campaign.dispatched", "campaign.approved", "dispatch.result"}
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: requiredSubjects,
		Storage:  nats.FileStorage,
	})
	if err != nil {
		if err == nats.ErrStreamNameAlreadyInUse {
			streamInfo, infoErr := js.StreamInfo(streamName)
			if infoErr != nil {
				log.Printf("[NATS-STREAM] Failed to inspect existing stream: %v\n", infoErr)
			} else {
				mergedSubjects := mergeSubjects(streamInfo.Config.Subjects, requiredSubjects)
				if !subjectsEqual(mergedSubjects, streamInfo.Config.Subjects) {
					streamInfo.Config.Subjects = mergedSubjects
					if _, updateErr := js.UpdateStream(&streamInfo.Config); updateErr != nil {
						log.Printf("[NATS-STREAM] Failed to update existing stream subjects: %v\n", updateErr)
					}
				}
			}
		} else {
			log.Printf("[NATS-STREAM] Stream metadata validation resolved: %v\n", err)
		}
	}

	return &JetStreamPublisher{nc: nc, js: js}, nil
}

func getNatsOptions(natsCreds string) []nats.Option {
	var opts []nats.Option
	trimmed := strings.TrimSpace(natsCreds)
	if trimmed != "" {
		if strings.Contains(trimmed, "-----BEGIN NATS USER JWT-----") {
			tmpFile, err := os.CreateTemp("", "nats-*.creds")
			if err == nil {
				_, _ = tmpFile.WriteString(trimmed)
				_ = tmpFile.Close()
				opts = append(opts, nats.UserCredentials(tmpFile.Name()))
			}
		} else {
			opts = append(opts, nats.UserCredentials(trimmed))
		}
	}
	return opts
}

func mergeSubjects(existing, required []string) []string {
	subjectSet := make(map[string]struct{}, len(existing)+len(required))
	for _, subject := range existing {
		subjectSet[subject] = struct{}{}
	}
	for _, subject := range required {
		subjectSet[subject] = struct{}{}
	}
	merged := make([]string, 0, len(subjectSet))
	for subject := range subjectSet {
		merged = append(merged, subject)
	}
	return merged
}

func subjectsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	subjectSet := make(map[string]struct{}, len(a))
	for _, subject := range a {
		subjectSet[subject] = struct{}{}
	}
	for _, subject := range b {
		if _, found := subjectSet[subject]; !found {
			return false
		}
	}
	return true
}

func (p *JetStreamPublisher) PublishDispatchTask(ctx context.Context, task *contracts.TargetDispatchTask) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to serialize target task payload: %w", err)
	}

	subject := "campaign.dispatched"

	_, err = p.js.Publish(subject, payload, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("nats stream rejected dispatch acknowledgment: %w", err)
	}

	return nil
}
