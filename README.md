# Go Engineering Starter (AI-First)

Starter สำหรับเริ่มโปรเจกต์ใหม่ โดยใช้ flow **คน design → AI implement → คน verify**
ใช้ได้ทั้ง **Antigravity** (อ่าน `.agents/` ตรง ๆ) และ **VS Code** (Copilot / Claude Code)

## โครงสร้าง
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
contracts/openapi.yaml               ← API truth; AI ห้ามแก้เองถ้าไม่ได้รับอนุญาต
Makefile                             ← make verify = gate เดียวก่อนบอกว่าเสร็จ
```

## เริ่มใช้งาน
```bash
gh repo create <new-project> --template Thapanut/go-engineering-starter --private --clone
cd <new-project> && go mod init <module>
make tools            # ติดตั้ง golangci-lint, gosec, govulncheck, redocly (+ brew install gitleaks)
make verify           # ต้องขึ้น RESULT: PASS — ถ้ามี gate ที่ไม่ได้รันจะขึ้น INCOMPLETE
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
- **Design & accountability อยู่ที่คน**: ADR, contract, spec เป็นของคุณ ส่วน AI เป็นผู้ลงมือทำ
- **Verify ด้วยหลักฐาน ไม่ใช่ความรู้สึก**: AC แต่ละข้อต้องมี test และต้องผ่าน gate ครบ (test/lint/gosec/govulncheck/gitleaks/contract)
- **Guardrail สำหรับธนาคาร**: ไม่ใช้ข้อมูลลูกค้าจริง, ใช้ idempotency + audit log, ไม่ใช้ float กับเงิน, deny by default
- **Reproducible**: prompt/rule/spec อยู่ใน git ทำให้ตรวจย้อนได้ว่า AI ได้รับคำสั่งอะไร
