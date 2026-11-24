# Spec rewrite progress (branch `spec-rewrite`)

Design docs: `/home/claude/specs/*.txt` (text of the 8 PDFs). HLD decisions: claude.ai project doc `claude/hld-decisions.md`.

Order: R1+D2 → D1 → R2+D3 → R3 → R4+D4 → R6+D6 → D5 → R5.
Per feature: backend (test-writer + implementer subagents, run here against Postgres) → apps (test-writer + implementer; verified via `gh workflow run` on this branch, Flutter can't install locally).

| Feature | Backend | Apps | Commit |
|---|---|---|---|
| R1+D2 Create a series, get a driver | done (30 integration tests green) | done (rider + driver CI green incl. emulator, APK, web) | done |
| D1 Driver registration and subscription | done (37 tests; 66 total green) | in progress | backend committed |
| R2+D3 Daily ride | – | – | – |
| R3 Skip, change, pause | – | – | – |
| R4+D4 Driver can't make it, time off | – | – | – |
| R6+D6 One-time rides | – | – | – |
| D5 Subscription renewal | – | – | – |
| R5 Account and cancelling | – | – | – |

## Token log (reported by subagents)
| Agent | Tokens |
|---|---|
| R1+D2 backend test writer | 139,073 |
| R1+D2 backend implementer | 331,786 |
| R1+D2 app test writer | 279,322 |
| R1+D2 app implementer | 493,884 |
| D1 backend test writer | 157,388 (post-cutoff part only) |
| D1 backend implementer | 231,403 (post-cutoff part only) |
| D1 app test writer | 261,379 |
| D1 app implementer | 330,991 |

Cutoff: 5-hour limit hit ~23:58 IST 23 Nov during R1+D2 apps; resumed 05:07 IST 24 Nov.

## Spec rulings (orchestrator)
- D1: first payment period = today to month end; null day_off valid; wrong code may be invalid_code; rider phone → 409 phone_in_use_as_rider at signup.
- R1: the start date is the first chosen weekday from tomorrow on. R1-I1's "Mon 23 Nov" is treated as a spec error.
- R1: rides are created for 14 days counted from the start date (Backend section), not the R1-I3 row's dates.
- Seed: Koramangala is (12.9352, 77.6245) and Whitefield is (12.9698, 77.7178).

Cutoff 2: 5-hour limit hit ~06:30 IST 24 Nov during D1 backend; resumed 10:08 IST.
