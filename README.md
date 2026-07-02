OmniPulse: Compliance-First Distributed Notification Engine
OmniPulse is a high-throughput, horizontally scalable notification orchestrator built in Go. It is architected to handle mass multi-platform messaging (WhatsApp Cloud API, Telegram Bot API, and X API) while strictly enforcing platform-specific compliance windows, rate limits, and schema validations.

Unlike naive broadcasting tools, OmniPulse decouples ingestion from external execution, utilizing a distributed message broker and localized state machines to guarantee system reliability and protect upstream platform tokens from velocity bans.

🛠️ System Architecture & Tech Stack
[ Next.js Dashboard ] ──(HTTP/WS)──► [ api-gateway (Go) ]
│
Enqueues Event Chunks
▼
[ NATS JetStream ]
│
┌─────────────────────┴─────────────────────┐
▼ ▼
[ compliance-engine (Go) ] [ broadcast-worker (Go) ]
(Redis Session Validation) (Concurrent Threaded Fleet)
│
Requests Quota
▼
[ rate-limiter (Go + Redis) ]
Frontend Control Plane: Next.js, TailwindCSS, TypeScript (Hosted on Vercel Free Tier)

API Gateway & Management Core: Go (net/http), PostgreSQL (Hosted on Neon/Supabase)

Distributed Event Broker: NATS JetStream (Containerized locally)

High-Speed Cache & Distributed Locking: Redis (Hosted on Upstash Serverless)

Execution Workspace: Go Monorepo utilizing native Go Workspaces (go.work)

Orchestration: Multi-stage Docker development environments (docker-compose.yml)

🛰️ Microservice Topography

1. apps/api-gateway (Core Router)
   The entry point for the frontend dashboard and external webhooks. It handles authentication, tenant workspace configurations, and campaign metadata. When a campaign triggers, it aggregates the targeted audience database keys, slices them into execution batches, and publishes them as immutable events to NATS JetStream.

2. apps/compliance-engine (Session State Machine)
   A highly optimized, state-aware boundary service. It listens to inbound user webhooks and maintains a rolling 24-hour interaction matrix in Redis. Before any outbound message is passed to external networks, this engine evaluates the target’s session status to dynamically enforce platform governance (e.g., routing free-form text vs. forcing strict Meta utility template schemas).

3. apps/broadcast-worker (High-Throughput Executor)
   The high-concurrency muscle of the architecture. It consumes execution batches from NATS JetStream and processes them across a fixed, bounded pool of goroutines. It leverages stream pipe optimizations to relay media assets directly from cloud object storage to third-party endpoints, preventing local memory allocation exhaustion.

4. apps/rate-limiter (Global Gateway Firewall)
   A dedicated isolation layer that intercepts all outgoing third-party network requests. Running an atomic Redis Lua script, it computes real-time global token buckets across all horizontal worker instances, dynamically throttling or backing off outbound traffic to match downstream platform quotas.

⚡ Production Real-World Solutions
Zero-RAM Media Pipelining: Workers do not read video files into application memory. Outbound attachments are processed via an io.TeeReader pipeline, streaming chunks directly to external endpoints to maintain a perfectly flat RAM profile.

Idempotent Dispatch Protections: Every batch execution carries an immutable cryptographic fingerprint. If a network partition causes a worker to drop offline, the message broker's retry loop will dispatch the task to a new node without risking double-messaging.
