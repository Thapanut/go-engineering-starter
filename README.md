# Go Engineering Starter

Base project สำหรับ Go service แบบ **Hexagonal (Ports & Adapters)** บน **Fiber v2 + GORM + PostgreSQL** พร้อม flow **คน design → AI implement → คน verify**
ใช้ได้ทั้ง **Antigravity** (อ่าน `.agents/` ตรง ๆ) และ **VS Code** (Copilot / Claude Code)

## สถาปัตยกรรม — [ADR-0002](docs/01-architecture/adr/0002-hexagonal-architecture.md), [ADR-0003](docs/01-architecture/adr/0003-fiber-and-gorm.md)
```
cmd/api/                         composition root: อ่าน config, เลือก adapter, wire เข้า core
cmd/devtoken/                    ออก JWT สำหรับ dev
internal/
  core/                          business rule ทั้งหมด — ไม่ import framework/DB/adapter
    domain/                        entity, value object (Money = int64 minor units), domain errors
    port/                          inbound (use case) และ outbound (TxManager, repositories, Clock, IDGenerator)
    service/                       implementation ของ use case
  adapter/
    inbound/httpapi/               Fiber v2: Module ต่อ feature, middleware (auth, trace id, timeout, recovery),
                                   body limit, strict JSON decode, central ErrorHandler → contract error code
    outbound/postgres/             GORM: TxManager (READ COMMITTED, rollback เมื่อ error/panic), pool, SQL log แบบไม่มีค่า parameter
    outbound/memory/               in-memory store (unit test / รันแบบไม่ต้องมี DB)
    outbound/system/               clock, UUID
  platform/                      config (env), logger (slog JSON), auth (JWT)
migrations/                      SQL (golang-migrate naming)
contracts/openapi.yaml           API truth
```
- dependency ชี้เข้าข้างในเท่านั้น: `adapter → port ← service → domain`
- ขอบเขตบังคับด้วย `depguard` ใน `.golangci.yml`: ถ้า `core` import Fiber, fasthttp, GORM, pgx, net/http, database/sql, `platform` หรือ `adapter` → `make lint` fail
- เปลี่ยน DB หรือเพิ่ม gRPC/Kafka consumer = เพิ่ม adapter ใหม่ ไม่ต้องแตะ core

## เพิ่ม feature ใหม่
1. `domain/` — entity, value object, error ของ feature
2. `port/<feature>.go` — inbound interface (use case) และ outbound repository interface; เพิ่ม field ใน `port.Repositories`
3. `service/` — implement use case ด้วย `TxManager.WithinTx` + unit test กับ memory adapter
4. `adapter/outbound/postgres` (GORM model ของ adapter เอง ไม่ใส่ tag ใน domain) + `memory` — implement repository; migration ใหม่ใน `migrations/` (ไม่ใช้ `AutoMigrate`)
5. `adapter/inbound/httpapi` — handler type ที่ implement `Module`, handler `return err` แล้ว map error ใน `errors.go`
6. `cmd/api/main.go` — wire service + ส่ง module เข้า `NewApp`
7. อัปเดต `contracts/openapi.yaml` ก่อน/พร้อมโค้ด แล้ว `make verify`

## รันบนเครื่อง
```bash
cp .env.example .env
make run            # PostgreSQL ใน Docker (port 55432) + API ที่ :8080
make run-memory     # หรือรันแบบไม่ต้องมี Docker
curl -s localhost:8080/healthz
curl -s localhost:8080/readyz
TOKEN=$(make -s token)     # JWT สำหรับเรียก /v1/*
```

ทดสอบ webhook → outbox → Kafka แบบ end-to-end (ต้องมี `curl` และ `openssl`):
```bash
make kafka-up                             # Kafka ใน Docker + สร้าง topic
make run KAFKA_BROKERS=localhost:9092     # terminal 1 (หรือใส่ KAFKA_BROKERS ใน .env)
make kafka-consume                        # terminal 2: ดู event ที่ถูก publish
make kafka-ui                             # หรือดูผ่านเว็บ: kafka-ui ที่ http://localhost:8081 (API ใช้ 8080)
make webhook-demo                         # terminal 3: สร้าง payment PENDING + ส่ง webhook ที่เซ็นแล้ว → PROCESSED
make webhook-demo                         # ส่งซ้ำ → DUPLICATE, ไม่มี event ใหม่
make webhook-demo INVOICE=INV-DEMO-0002 AMOUNT=500.00 RESP=4001   # payment ใหม่ → FAILED
```
ถ้า DB ถูกสร้างไว้ก่อนมี `migrations/0002_outbox` หรือ `0003_payment_checkout` ให้ `make db-reset` ก่อน (ลบข้อมูล dev)

หน้า demo checkout → webhook → polling ในเบราว์เซอร์ ([flow](docs/02-specs/frontend-integration-flow.md)):
```bash
make demo STORE=memory                    # ไม่ต้องใช้ Docker; เปิด URL ที่ print ออกมา (http://localhost:8080/demo#…)
make demo                                 # หรือใช้ PostgreSQL; เพิ่ม KAFKA_BROKERS=localhost:9092 หลัง `make kafka-up`
```
เลือกสินค้า → **Checkout with 2C2P** (backend คิดราคาเอง) → หน้า mock 2C2P → **Simulate Successful/Failed Payment** → หน้า return ขึ้น *Verifying…* และ poll ทุก 2 วินาที ขณะที่ webhook (เซ็น HS256) ตามมาใน 3 วินาที → **Payment Successful! Order Confirmed**; กล่อง Live debug แสดง DB status, webhook ล่าสุด และ event ใน outbox/Kafka
JWT และ 2C2P sandbox key ส่งไปใน URL fragment (ไม่ถูกส่งไป server); `/demo` เปิดเฉพาะเมื่อ `DEMO_UI_ENABLED=true`

## เริ่มโปรเจกต์ใหม่จาก template
```bash
gh repo create <new-project> --template Thapanut/go-engineering-starter --private --clone
cd <new-project>
go mod edit -module github.com/<org>/<new-project>
grep -rl 'github.com/Thapanut/go-engineering-starter' --include='*.go' --include='.golangci.yml' . | xargs sed -i '' 's#github.com/Thapanut/go-engineering-starter#github.com/<org>/<new-project>#g'
make tools            # golangci-lint, gosec, govulncheck, redocly (+ brew install gitleaks)
make verify           # ต้องขึ้น RESULT: PASS — gate ที่ไม่ได้รันจะขึ้น INCOMPLETE
```

## Quality gates (`make verify`, รันใน CI ทุก PR)
build · unit test (race) · integration test (PostgreSQL) · golangci-lint (+depguard) · gosec · govulncheck · gitleaks · OpenAPI lint

## AI tooling
```
AGENTS.md                      ← คู่มือกลางของ AI ทุกตัว (roles, rules, commands)
CLAUDE.md, .github/copilot-instructions.md  ← ชี้กลับมา AGENTS.md
.claude/skills → .agents/skills          ← symlink ให้ Claude Code
.agents/
  rules/      always-on: roles, security (banking/PDPA), engineering standards
  skills/     /design → /spec → /implement → /verify  (+ write-adr, threat-model)
docs/
  00-business/problem-statement.md   ← ปัญหา, metric, constraint
  01-architecture/architecture.md    ← ออกแบบ (AI ร่างได้ คน approve)
  01-architecture/adr/               ← เหตุผลของทุก decision
  02-specs/                          ← spec + acceptance criteria
  03-verification/verification-log.md ← หลักฐานการ verify
```

**Antigravity:** เปิดโฟลเดอร์ได้เลย ระบบอ่าน `AGENTS.md`, `.agents/rules/` และ `.agents/skills/` ให้เอง
> หมายเหตุ: Antigravity เลิกใช้ Workflows วันที่ 1 พ.ย. 2026 kit นี้เลยใช้ Skills แทนทั้งหมด
> (workspace AUCT ที่ยังใช้ `.agents/workflows/` ให้รัน `/migrate-workflows` ก่อนถึงวันนั้น)

**VS Code:**
- Copilot อ่าน `AGENTS.md` และ `.github/copilot-instructions.md` อัตโนมัติ (setting อยู่ใน `.vscode/settings.json`)
- Rules ใน `.agents/rules/` ถูก symlink ไปที่ `.github/instructions/*.instructions.md` แล้ว Copilot จึงใช้กฎชุดเดียวกับ Antigravity (แก้ที่ `.agents/rules/` ที่เดียวพอ)
- Copilot อ่าน `.agents/skills/` เองและเรียกเป็น slash command ได้ (`/design`, `/spec`, ...) **ไม่ต้อง symlink**
- Claude Code อ่าน skills จาก `.claude/skills` ซึ่ง symlink ไปที่ `.agents/skills` ให้แล้ว (แก้ที่ `.agents/skills/` ที่เดียวพอ)

## Flow การทำงาน 1 feature
| ขั้น | ใครทำ | คำสั่ง | Output |
|---|---|---|---|
| 1. เข้าใจปัญหา | คน | เขียน problem-statement | metric + constraint |
| 2. ออกแบบ | คนตัดสินใจ, AI เสนอ option | `/design` | ADR + architecture + contract diff |
| 3. แตก spec | AI ร่าง, คน approve | `/spec` | `docs/02-specs/x.md` status APPROVED |
| 4. Implement | AI | `/implement` | code + test ต่อ AC |
| 5. Verify | AI รัน gate, **คน review** | `/verify` | Verification Report + log |
| 6. Merge | คน | PR | human sign-off |
