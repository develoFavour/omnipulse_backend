package domain

import "context"

// ComplianceRepository dictates our sub-millisecond key-value storage capabilities (Driven Port)
type ComplianceRepository interface {
	IsOptedOut(ctx context.Context, platform, routingValue string) (bool, error)
	SetOptOutStatus(ctx context.Context, platform, routingValue string, optedOut bool) error
}
