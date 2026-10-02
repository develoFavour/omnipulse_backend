# OmniPulse: Compliance-First Distributed Notification Engine

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go Version" />
  <img src="https://img.shields.io/badge/NATS-JetStream-27AAE1?style=for-the-badge&logo=natsdotio&logoColor=white" alt="NATS JetStream" />
  <img src="https://img.shields.io/badge/Redis-7.x-DC382D?style=for-the-badge&logo=redis&logoColor=white" alt="Redis" />
  <img src="https://img.shields.io/badge/PostgreSQL-16-4169E1?style=for-the-badge&logo=postgresql&logoColor=white" alt="PostgreSQL" />
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?style=for-the-badge&logo=docker&logoColor=white" alt="Docker" />
  <img src="https://img.shields.io/badge/Architecture-Hexagonal_%2F_DDD-6366F1?style=for-the-badge" alt="Architecture" />
  <img src="https://img.shields.io/badge/License-MIT-green?style=for-the-badge" alt="License" />
</p>

---

## Executive Summary

**OmniPulse** is a high-throughput, horizontally scalable, event-driven notification engine built in **Go**. Architected for enterprise-scale multi-platform broadcasting (**WhatsApp Cloud API, Telegram Bot API, and X API**), it strictly enforces upstream compliance windows, global token-bucket rate limits, and schema validations.

Unlike naive broadcasting scripts that fire unbounded HTTP requests, OmniPulse decouples web-tier ingestion from distributed external dispatch. It employs **NATS JetStream** as an immutable distributed event backbone, stateful **Redis** session matrixes to guarantee platform compliance, and memory-bounded goroutine pipelines that eliminate heap exhaustion during high-volume media delivery.

---

## System Architecture

```mermaid
flowchart TB
    subgraph ClientLayer ["Client & Control Plane"]
        UI["Next.js Control Plane<br/>(TailwindCSS + WebSockets)"]
        WH["Inbound Webhooks<br/>(Meta / Telegram / X)"]
    end

    subgraph GatewayLayer ["Ingestion & Edge Gateway"]
        APIGW["apps/api-gateway (Go)<br/>- Tenant Isolation & RBAC<br/>- Campaign Scheduler<br/>- Audience Chunking Engine"]
        DB[("PostgreSQL 16<br/>(Tenants, Contacts, Audits)")]
    end

    subgraph EventFabric ["Distributed Event Backbone"]
        NATS{{"NATS JetStream<br/>(Persistent Message Broker)<br/>• Stream: CAMPAIGNS<br/>• Consumer: WORKER_GROUP"}}
    end

    subgraph GovernanceLayer ["State & Compliance Engine"]
        COMP["apps/compliance-engine (Go)<br/>- 24h Rolling Window State Machine<br/>- Template Schema Enforcement"]
        RCACHE[("Redis 7 Speed Layer<br/>• Rolling Session Matrix<br/>• Cryptographic Deduplication<br/>• Distributed Lock")]
    end

    subgraph ExecutionLayer ["Worker Fleet & Rate Limiter"]
        WORKER["apps/broadcast-worker (Go)<br/>- Bounded Worker Pool<br/>- Zero-RAM io.Pipe Streaming<br/>- Auto Retry & Exponential Backoff"]
        RLIMIT["Global Rate Limiter<br/>(Atomic Redis Lua Token Bucket)"]
    end

    subgraph Downstream ["Third-Party Egress Networks"]
        WABA["WhatsApp Cloud API<br/>(Meta Graph API)"]
        TG["Telegram Bot API"]
        XAPI["X (Twitter) API v2"]
    end

    subgraph Telemetry ["Live Real-Time Telemetry"]
        WS["WebSocket Hub (/api/v1/ws/campaigns/:id)<br/>Real-Time Delivery Audits"]
    end

    %% Flow connections
    UI -->|"HTTP / REST API"| APIGW
    WH -->|"Status / Inbound Messages"| COMP
    APIGW <-->|"CRUD / Audience Queries"| DB
    APIGW -->|"Publish Batched Tasks (250 items/chunk)"| NATS
    
    NATS -->|"Pull Execution Batches"| WORKER
    WORKER -->|"Interrogate Session State"| COMP
    COMP <-->|"Query/Update 24h Interaction"| RCACHE
    
    WORKER -->|"Request Egress Token"| RLIMIT
    RLIMIT <-->|"Atomic Token Bucket (Lua)"| RCACHE
    
    WORKER -->|"Zero-Copy Stream"| WABA
    WORKER -->|"Dispatched Message"| TG
    WORKER -->|"Direct Post"| XAPI

    WORKER -->|"Delivery Audit Events"| DB
    WORKER -.->|"Live Progress Telemetry"| WS
    WS -.->|"Push Updates"| UI

    classDef primary fill:#1e293b,stroke:#6366f1,stroke-width:2px,color:#f8fafc;
    classDef storage fill:#0f172a,stroke:#38bdf8,stroke-width:2px,color:#f8fafc;
    classDef broker fill:#1e1b4b,stroke:#a855f7,stroke-width:2px,color:#f8fafc;
    classDef egress fill:#064e3b,stroke:#10b981,stroke-width:2px,color:#f8fafc;

    class APIGW,COMP,WORKER primary;
    class DB,RCACHE storage;
    class NATS broker;
    class WABA,TG,XAPI egress;
```

---

## Campaign Execution Lifecycle

```mermaid
sequenceDiagram
    autonumber
    actor Admin as Workspace Admin
    participant Gateway as API Gateway
    participant Postgres as PostgreSQL
    participant NATS as NATS JetStream
    participant Worker as Broadcast Worker
    participant Comp as Compliance Engine
    participant Redis as Redis Cache
    participant Meta as WhatsApp / External API
    participant WS as WebSocket Hub

    Admin->>Gateway: POST /api/v1/campaigns (Create & Schedule)
    Gateway->>Postgres: Persist Campaign (Status: PENDING)
    Admin->>Gateway: POST /api/v1/campaigns/:id/dispatch
    Gateway->>Postgres: Stream Targeted Audience Keys
    Note over Gateway: Slices 50,000 audience records<br/>into 250-item immutable batches
    Gateway->>NATS: Publish Batched Events (Stream: CAMPAIGNS)
    Gateway-->>Admin: 202 Accepted (Campaign IN_FLIGHT)

    loop Worker Execution
        NATS->>Worker: Pull Batch Task
        Worker->>Comp: Validate Target Session (Contact ID)
        Comp->>Redis: Check 24h Interaction Timestamp
        alt Within 24-Hour Window
            Redis-->>Worker: Session ACTIVE -> Free-form payload permitted
        else Outside 24-Hour Window
            Redis-->>Worker: Session EXPIRED -> Force Meta Utility Template Schema
        end

        Worker->>Redis: Atomic Token Request (Lua Rate Limiter)
        Redis-->>Worker: Quota Granted (Token Leased)

        Worker->>Meta: Stream Payload (Zero-RAM io.Pipe)
        Meta-->>Worker: 200 OK (Message ID, Timestamp)

        Worker->>Postgres: Record Delivery Audit (SENT / DELIVERED)
        Worker->>WS: Broadcast Live Progress Metric
        WS-->>Admin: Real-time UI progress increment (WebSocket)
        Worker->>NATS: Explicit ACK (Acknowledge Message)
    end
```

---

## Senior Engineering Highlights

### 1. Zero-Allocation Media Pipelining
Broadcasting attachments (videos, documents, audio) naively leads to memory exhaustion when thousands of goroutines read multi-megabyte files into heap RAM simultaneously. 
OmniPulse implements zero-copy stream processing utilizing `io.TeeReader` and Go pipes (`io.Pipe`):
```go
// Direct streaming pipeline from Cloud Object Storage to External API endpoint
pr, pw := io.Pipe()
writer := multipart.NewWriter(pw)

go func() {
    defer pw.Close()
    defer writer.Close()
    part, _ := writer.CreateFormFile("file", filename)
    // Stream directly through without buffering entire payload in memory
    io.Copy(part, cloudMediaStream)
}()

req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, pr)
req.Header.Set("Content-Type", writer.FormDataContentType())
```
* **Impact**: Heap allocation remains completely flat ($O(1)$ RAM profile) regardless of attachment size or concurrency tier.

### 2. Meta 24-Hour Window Compliance State Machine
Meta strictly bans business numbers that transmit free-form text outside of customer-initiated 24-hour windows.
* The **Compliance Engine** intercepts inbound webhook events and records epoch timestamps in Redis sorted sets.
* Before outbound dispatch, tasks are dynamically rewritten: if expired, free-form text is translated into approved pre-registered template schemas with parameter slots (`{{1}}`, `{{2}}`).
* Protects client tokens from velocity bans and platform violations automatically.

### 3. Distributed Atomic Token Bucket Rate Limiter
To enforce global rate limits across horizontally auto-scaled worker instances, rate quota checks are executed inside Redis via atomic **Lua scripts**:
* Guarantees race-condition-free token consumption in single-digit microseconds without distributed lock contention.
* If a platform quota drops or returns `429 Too Many Requests`, worker nodes back off with jittered exponential delay across all nodes.

### 4. Idempotent Deduplication & At-Least-Once Delivery
* Every batch delivery task is stamped with a deterministic SHA-256 fingerprint (`tenant_id + campaign_id + contact_id + step`).
* Workers execute an atomic `SETNX` against Redis before egress.
* If a network partition causes a worker node to drop offline mid-flight, NATS JetStream requeues unacknowledged messages after consumer lease timeout without duplicating already-sent messages.

### 5. Standardized Unified API Envelope
The backend strictly adheres to a uniform API communication protocol:
```json
// Success Response
{
  "success": true,
  "data": { "id": "cmp_01J...", "status": "dispatched" }
}

// Error Response
{
  "success": false,
  "error": "Campaign quota exceeded for current billing cycle",
  "code": "QUOTA_EXCEEDED"
}
```
All HTTP handlers use type-safe Go helpers (`WriteJSON`, `WriteError`, `WriteAppError`, `WriteNoContent`) preventing leaking unhandled raw error structs to clients.

---

## Monorepo Topography

```
omnipulse/
├── apps/
│   ├── api-gateway/            # Primary HTTP API & Webhook Edge
│   │   ├── cmd/                # Entrypoint (main.go)
│   │   └── internal/
│   │       ├── config/         # Environment & runtime configurations
│   │       ├── domain/         # Domain entities & interfaces (DDD)
│   │       ├── handler/        # HTTP controllers & WebSocket endpoints
│   │       ├── repository/     # PostgreSQL data access layer
│   │       ├── service/        # Core business & orchestration logic
│   │       └── utils/          # Standardized response envelopes & helpers
│   ├── broadcast-worker/       # High-throughput egress execution engine
│   │   ├── cmd/                # Worker daemon entrypoint
│   │   └── internal/
│   │       ├── config/         # Worker concurrency & broker config
│   │       └── worker/         # Bounded goroutine pool & API dispatchers
│   └── compliance-engine/      # Session state machine & governance
│       ├── cmd/                # Compliance service entrypoint
│       └── internal/
│           ├── domain/         # Session rules & template validations
│           ├── repository/     # Redis sorted set matrix
│           └── worker/         # Inbound webhook consumer
├── shared/
│   └── contracts/              # Shared Go types, events, and NATS topics
├── infra/
│   ├── docker/                 # Container definitions
│   └── postgres/
│       ├── migrations/         # SQL migration files (000001 - 000004)
│       └── seeds/              # Development dataset seeds
├── docker-compose.yml          # Local infra (Postgres 16, Redis 7, NATS JetStream)
├── Makefile                    # Automation workflows & hot-reload targets
└── go.work                     # Go multi-module workspace
```

---

## API Specification (Core Endpoints)

| Method | Endpoint | Description | Auth |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/v1/campaigns` | Create and configure a new broadcast campaign | Clerk JWT |
| `GET` | `/api/v1/campaigns` | List campaigns with status, pagination, and filters | Clerk JWT |
| `GET` | `/api/v1/campaigns/:id` | Fetch detailed campaign metadata & metrics | Clerk JWT |
| `POST` | `/api/v1/campaigns/:id/dispatch` | Enqueue campaign for immediate distributed dispatch | Clerk JWT |
| `POST` | `/api/v1/campaigns/:id/schedule` | Schedule campaign execution for a future ISO timestamp | Clerk JWT |
| `DELETE`| `/api/v1/campaigns/:id/schedule` | Cancel scheduled dispatch | Clerk JWT |
| `GET` | `/api/v1/campaigns/:id/stats` | Aggregated delivery metrics (sent, delivered, failed) | Clerk JWT |
| `GET` | `/api/v1/ws/campaigns/:id` | WebSocket real-time delivery telemetry stream | Token |
| `GET` | `/api/v1/team/members` | Retrieve workspace team members and roles | Clerk JWT |
| `POST` | `/api/v1/team/invite` | Send workspace invitation email with cryptographic token | Clerk JWT |
| `GET` | `/api/v1/invitations/preview` | Public preview of an invitation token | Public |
| `POST` | `/api/v1/invitations/accept` | Accept workspace invitation and join tenant | Clerk JWT |
| `GET` | `/api/v1/channels` | Retrieve connected WhatsApp, Telegram, and X channels | Clerk JWT |
| `POST` | `/api/v1/channels` | Connect new credentials & configure platform tokens | Clerk JWT |

---

## Local Development & Setup

### Prerequisites
* **Go**: `v1.23+` (configured with `go.work`)
* **Docker & Docker Compose**: `v24+`
* **Make**: (GNU Make or equivalent)
* **Air**: (optional, for hot-reloading: `go install github.com/air-verse/air@latest`)

### 1. Clone & Setup Workspace
```bash
git clone https://github.com/develoFavour/omnipulse_backend.git
cd omnipulse_backend
```

### 2. Boot Infrastructure Containers
Spin up PostgreSQL 16, Redis 7, and NATS JetStream in daemon mode:
```bash
make up
# Or: docker compose up -d
```

### 3. Run Database Migrations & Seeds
```bash
make db-migrate
make db-seed
```

### 4. Run Services Locally
```bash
# Terminal 1: API Gateway (Port 8080)
make dev-api

# Terminal 2: Compliance Engine
make dev-compliance

# Terminal 3: Broadcast Worker Fleet
make dev-broadcast
```

### 5. Verify Health
```bash
curl http://localhost:8080/health
# Response: {"status":"healthy","service":"api-gateway"}
```

---

## Environment Configuration

Create a `.env` file at the root or inject via your orchestrator:

| Variable | Description | Example |
| :--- | :--- | :--- |
| `PORT` | API Gateway HTTP Port | `8080` |
| `DATABASE_URL` | PostgreSQL connection string | `postgres://admin:secretpassword@localhost:5433/omnipulse_dev?sslmode=disable` |
| `REDIS_URL` | Redis instance connection URI | `redis://localhost:6379` |
| `NATS_URL` | NATS JetStream broker URL | `nats://localhost:4222` |
| `CLERK_SECRET_KEY` | Clerk Authentication Secret | `sk_test_...` |
| `CLERK_PEM_PUBLIC_KEY`| Clerk JWT verification public key | `-----BEGIN PUBLIC KEY...` |
| `META_GRAPH_API_URL` | Meta Graph API base endpoint | `https://graph.facebook.com/v20.0` |

---

## License

OmniPulse is open-source software licensed under the [MIT License](LICENSE).
