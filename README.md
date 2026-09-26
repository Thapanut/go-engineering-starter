# Go Engineering Starter (AI-First)

Starter สำหรับเริ่มโปรเจกต์ใหม่ โดยใช้ flow **คน design → AI implement → คน verify**
ใช้ได้ทั้ง **Antigravity** (อ่าน `.agents/` ตรง ๆ) และ **VS Code** (Copilot / Claude Code)

## สถาปัตยกรรม: Hexagonal (Ports & Adapters) — [ADR-0002](docs/01-architecture/adr/0002-hexagonal-architecture.md)
```
cmd/api/                         composition root: อ่าน config, เลือก adapter, wire เข้า core
cmd/devtoken/                    ออก JWT สำหรับ dev
internal/
  core/                          ← หัวใจ: business rule ทั้งหมด ไม่ import framework/DB
    domain/                        Money (int64 satang), Account, Transfer, AuditEntry, errors
    port/                          inbound: TransferUseCase, AccountQuery
                                   outbound: AccountRepository, TransferRepository, AuditRepository, TxManager, Clock, IDGenerator
    service/                       use case: TransferService (idempotency, ownership, audit)
  adapter/
    inbound/httpapi/               Gin handlers, DTO, middleware (auth, trace id, timeout, body limit), error → contract code
    outbound/postgres/             pgx, SELECT … FOR UPDATE, unique idempotency index
    outbound/memory/               in-memory (unit test + run แบบไม่ต้องมี DB)
    outbound/system/               clock, UUID
  platform/                      config (env), logger (slog JSON), auth (JWT)
migrations/                      SQL (golang-migrate naming) + dev seed ข้อมูลสังเคราะห์
contracts/openapi.yaml           API truth
```
- dependency ชี้เข้าข้างในเท่านั้น: `adapter → port ← service → domain`
- ขอบเขตถูกบังคับด้วย `depguard` ใน `.golangci.yml` — ถ้า core import Gin/pgx/adapter, `make lint` จะ fail ทันที
- เปลี่ยน DB หรือเพิ่ม gRPC/Kafka consumer = เพิ่ม adapter ใหม่ ไม่ต้องแตะ core

## ลองรัน (5 นาที)
```bash
cp .env.example .env
make run                 # เปิด PostgreSQL ใน Docker (port 55432) + API ที่ :8080  (หรือ make run-memory ไม่ต้องมี Docker)
TOKEN=$(make -s token SUB=demo-alice)
curl -s -H "Authorization: Bearer $TOKEN" localhost:8080/v1/accounts/a1111111-1111-4111-8111-111111111111
curl -s -i -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: demo-1" \
  -d '{"fromAccountId":"a1111111-1111-4111-8111-111111111111","toAccountId":"b1111111-1111-4111-8111-111111111111","amount":{"amount":150000,"currency":"THB"}}' \
  localhost:8080/v1/transfers    # ยิงซ้ำด้วย key เดิม → 200 + Idempotent-Replayed: true, ไม่ตัดเงินซ้ำ
```

## Reference feature: โอนเงินภายในธนาคาร
[Spec](docs/02-specs/intra-bank-transfer.md) มี AC 16 ข้อ ทุกข้อมี test ชื่อ `TestAC<NN>_…`
| สิ่งที่ธนาคารต้องการ | ทำที่ไหน |
|---|---|
| ยิงซ้ำไม่ตัดเงินซ้ำ | `Idempotency-Key` + fingerprint ของ request + unique index `(requested_by, idempotency_key)` |
| ไม่ติดลบ / ไม่ race | `SELECT … FOR UPDATE` เรียงตาม id (กัน deadlock) + `CHECK (balance >= 0)` |
| Audit | เขียน `audit_log` (before/after, actor, trace id) ใน transaction เดียวกัน, DB trigger กันแก้/ลบ |
| เงินไม่ใช้ float | `domain.Money{Amount int64}` หน่วยสตางค์ |
| กัน IDOR / enumeration | บัญชีของคนอื่นตอบ 404 เหมือนไม่มีอยู่ |
| ไม่มี PII ใน log | log แค่ route template, status, latency, trace id |

## เริ่มโปรเจกต์ใหม่จาก template
```bash
gh repo create <new-project> --template Thapanut/go-engineering-starter --private --clone
cd <new-project>
go mod edit -module github.com/<org>/<new-project>
grep -rl 'github.com/Thapanut/go-engineering-starter' --include='*.go' --include='.golangci.yml' . | xargs sed -i '' 's#github.com/Thapanut/go-engineering-starter#github.com/<org>/<new-project>#g'
make tools            # golangci-lint, gosec, govulncheck, redocly (+ brew install gitleaks)
make verify           # ต้องขึ้น RESULT: PASS — gate ที่ไม่ได้รันจะขึ้น INCOMPLETE
```
แล้วแทน reference feature ด้วยโดเมนจริงผ่าน `/design → /spec → /implement → /verify`

## AI tooling
```
AGENTS.md                      ← คู่มือกลางของ AI ทุกตัว (roles, rules, commands)
CLAUDE.md, .github/copilot-instructions.md  ← ชี้กลับมา AGENTS.md
.claude/skills → .agents/skills          ← symlink ให้ Claude Code
.agents/
  rules/      always-on: roles, security (banking/PDPA), engineering standards
  skills/     /design → /spec → /implement → /verify  (+ write-adr, threat-model)
docs/
  00-business/problem-statement.md   ← คุณเขียน: ปัญหา, metric, constraint
  01-architecture/architecture.md    ← คุณออกแบบ (AI ร่างได้ คุณ approve)
  01-architecture/adr/               ← เหตุผลของทุก decision
  02-specs/                          ← spec + acceptance criteria (สัญญาระหว่างคุณกับ AI)
  03-verification/verification-log.md ← หลักฐานว่าคุณ verify แล้ว
Makefile                             ← make verify = gate เดียวก่อนบอกว่าเสร็จ (รันใน CI ด้วย)
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
| 1. เข้าใจปัญหา | คุณ | เขียน problem-statement | metric + constraint |
| 2. ออกแบบ | คุณตัดสินใจ, AI เสนอ option | `/design` | ADR + architecture + contract diff |
| 3. แตก spec | AI ร่าง, คุณ approve | `/spec` | `docs/02-specs/x.md` status APPROVED |
| 4. Implement | AI | `/implement` | code + test ต่อ AC |
| 5. Verify | AI รัน gate, **คุณ review** | `/verify` | Verification Report + log |
| 6. Merge | คุณ | PR | human sign-off |

## หลักที่ใช้ตอบกรรมการ
- **Architecture บังคับด้วยเครื่อง ไม่ใช่แค่เอกสาร**: hexagonal boundary ถูกตรวจโดย linter ทุก PR
- **Design & accountability อยู่ที่คน**: ADR, contract, spec เป็นของคุณ ส่วน AI เป็นผู้ลงมือทำ
- **Verify ด้วยหลักฐาน ไม่ใช่ความรู้สึก**: AC แต่ละข้อต้องมี test และต้องผ่าน gate ครบ (test/integration/lint/gosec/govulncheck/gitleaks/contract) ทั้งบนเครื่องและใน CI
- **Guardrail สำหรับธนาคาร**: ไม่ใช้ข้อมูลลูกค้าจริง, ใช้ idempotency + audit log, ไม่ใช้ float กับเงิน, deny by default
- **Reproducible**: prompt/rule/spec อยู่ใน git ทำให้ตรวจย้อนได้ว่า AI ได้รับคำสั่งอะไร
