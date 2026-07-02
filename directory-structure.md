omnipulse/
├── go.work # Native Go workspace orchestrator
├── Makefile # Automation shortcuts (make up, make test, make seed)
├── docker-compose.yml # Local infrastructure setup (NATS, App Workers)
├── apps/
│ ├── web-dashboard/ # Next.js Frontend app
│ ├── api-gateway/ # Go Module 1: HTTP API Router & Auth
│ ├── compliance-engine/ # Go Module 2: Session tracker & template logic
│ └── broadcast-worker/ # Go Module 3: High-throughput outbound worker pool
└── shared/
└── contracts/ # Shared Go structs or Protobuf generated code
