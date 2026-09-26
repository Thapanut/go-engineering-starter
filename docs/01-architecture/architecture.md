# Architecture  _(Human-owned; AI may draft, human approves)_

## 1. Context (C4 L1)
```mermaid
flowchart LR
  user([Customer]) --> app[Mobile/Web App]
  app --> gw[API Gateway]
  gw --> svc[Core Service]
  svc --> db[(PostgreSQL)]
  svc --> mq{{Kafka}}
  mq --> worker[Worker]
  svc --> ext[[External / Core Banking]]
```

## 2. Containers & responsibilities
| Container | Responsibility | Tech | Owner |
|---|---|---|---|

## 3. Key flows
<Sequence diagrams for the 2–3 riskiest flows.>

## 4. Non-functional requirements
| NFR | Target |
|---|---|
| Availability | 99.9% |
| Latency p95 | < 300 ms |
| RPO / RTO | |
| Security | AuthN/Z, encryption in transit & at rest, audit log |

## 5. Decisions
- [ADR-0001 Record architecture decisions](adr/0001-record-architecture-decisions.md)

## 6. Risks & mitigations
| Risk | Impact | Mitigation |
|---|---|---|
