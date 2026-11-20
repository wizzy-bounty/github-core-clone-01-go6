# Anvil-Go v5 — performance notes

Goal: beat v4 locally by a LARGE margin (server sandbox inflates p50 ~2.2x;
v4 local 976–990 → server 927, perf 127, p50 555.917µs; prize bar #20 server:
total 959, perf 159, p50 359.556µs).

## Scoring (kit calibration.json T_ref, ms)
points(workload) = clamp(25 − 12.5·log2(t/T_ref), 0, 50), median of 3 rounds.
- push  T_ref 1640.820 ms (200 sequential receive-pack, 1 KiB change)
- clone T_ref  266.941 ms (50 concurrent depth-1 clones of 200-commit repo)
- read  T_ref 2759.715 ms (2000 sequential GET issue, one keep-alive conn, undici)
- write T_ref  348.965 ms (200 sequential POST issue)
perf_p50_us = geomean of the four medians × 1000.

Points for 50/48/46 per workload need t ≤ T_ref × {0.250 / 0.280 / 0.315}.

## v4 baseline (5 load-gated grade-local runs, 2026-11-20 ~16:10 WIB)
Totals: 983 / 981 / 990 / 976 / 979 (perf 183/181/190/176/179, p50 241/254/210/257/254 µs)
Per-workload p50 medians (ms) across the 5 runs:
- push:  325 / 284 / 280 / 367 / 337  → 50 pts every run (capped; ≤440ms keeps 50)
- clone:  93 / 111 /  66 / 104 /  97  → 41–50 pts (volatile; 50 needs ≤66.7ms)
- read:  804 / 885 / 812 / 811 / 763  → 46–48 pts (50 needs ≤689.9ms)
- write: 171 / 157 / 130 / 134 / 137  → 38–43 pts (weakest; 48 needs ≤97.5ms, 50 ≤87.2ms)

## Micro-bench floor (this VM, load ~3.6, Go raw client, keep-alive)
- GET /healthz: ~170 µs/req — transport+scheduling floor dominates handler cost
- GET issue (view-cached): ~158 µs/req — handler logic is noise vs the floor locally;
  wins must come from CPU-demand reduction (shared-CPU queueing) and parallelism.

## Structural findings (v4 code)
- ALL git HTTP (info/refs, upload-pack, receive-pack) and ALL REST run under ONE
  global store mutex → 50 concurrent clones fully serialize (~1.8ms lock-hold each
  incl. socket write while holding the lock). Prime suspect for clone time.
- Read path already serves a cached issue serialization (viewCache); remaining
  per-request cost is net/http machinery + header block + fmt.Sprintf request IDs.
- Write path: generic map JSON parse + ~40-field map view build + json.Marshal.

## Iterations
- v5.0 = byte-identical copy of v4 (commit ed74958).
- v5.1 (locking): store mutex → RWMutex; git info/refs + upload-pack serve
  under the READ lock (exact-key lookup, write-lock fallback for adoption);
  packRespCache guarded by its own RWMutex. Clone sanity: 50/50 git-CLI
  shallow clones OK.
- v5.2 (fastpath.go): raw-TCP front end. GET issue cache hits served raw;
  issue POSTs + cache misses run the exact generic handler via a capture
  ResponseWriter; everything else delegates to net/http with byte replay
  (prefixConn). Kill switch: ANVIL_NO_FASTPATH=1.
- v5.3 (fast create + info cache): POST create handled directly in the fast
  path with a hand-rolled issue serializer (falls back to the generic
  handler on unknown repo / empty title / unknown label — i.e. whenever the
  generic code would produce an error). Equivalence harness vs the stock
  path: 17/17 ops value-identical (labels string+object form, assignees,
  escapes/unicode/U+2028, extra fields, 422s, malformed JSON, GETs).
  git info/refs advertisements cached by (service, protocol, head branch,
  refs fingerprint, repo id). GOMAXPROCS env-tunable (ANVIL_GOMAXPROCS,
  default 3); a =2 probe showed no clear difference.

## Grade results (load-gated starts ≤2.0; totals / perf / p50µs)
IMPORTANT: the box degraded between the v4 baseline window (~16:10 WIB) and
these runs (~17:30–18:55 WIB). A same-conditions control re-grade of STOCK
v4 scored 961–971 (read 1221–1505ms) vs its 976–990 baseline earlier — so
v5 must be judged against the control, not the morning baseline.
- v4 control ×3: 963 / 971 / 961 — push 324–358 (50), clone 113–129 (38–41),
  read 1221–1505 (36–40), write 150–182 (37–40)
- v5.2 ×2: 962 / 963 — clone 143–157, read 1386–1394, write 150–153 (40/40)
- v5.3 ×3: 957 / 963 / 960 — clone 127–148, read 1330–1475, write 176–227
- v5.3 GOMAXPROCS=2 ×2: 960 / 951 (no clear effect; env may not propagate)
- v5.3 final shipped binary ×2: 954 / 965 — push 364/430, clone 141/120,
  read 1409/1373, write 271/161
Feature 600/600 and agent 200/200 in EVERY run (v4 control and all v5).

## Honest conclusion
Locally, v5 ≈ v4 within run-to-run noise (±25% per workload on this shared
box) — the stretch targets (perf ≥195, p50 ≤180µs) are NOT demonstrable on
this VM right now, for v4 either. The v5 changes are strictly CPU-reducing
on the server side (fewer syscalls/goroutines per hot request, no map
building or reflection on create, cached git advertisements, parallel git
reads) and value-identical in behavior; on the dedicated 2-CPU sandbox —
where the suite and server are the only tenants — that CPU reduction is
what the score rewards. Local micro-bench (Go client): write ~13% faster
than stock (334–380 vs 377–440 µs/req); read at the transport floor both
ways (~160–170 µs/req raw).

## v6

v6 is the v5 code, byte for byte — no logic or perf changes. It exists
because v5 scored 0 on the server, and the root cause had nothing to do
with the code: the v5 tree was committed WITHOUT the compiled `anvil-go`
binary. `main.ts` only spawns `./anvil-go`, and the grading sandbox has
no install or build step (kit RULES.txt) — a committed statically linked
binary runs as vendored source, but nothing ever runs `go build`. The
launcher exited instantly, so every server subscore was 0. The unstripped
v5 binary was 10.275.461 bytes, over the 8.388.608-byte per-file cap,
which is why it had been removed.

Fix: rebuild the same source with strip flags and commit the binary.

- Build command:
  `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o anvil-go .`
- Resulting binary: 7.155.896 bytes — under the 8.388.608 cap.
  `file`: ELF 64-bit LSB executable, x86-64, statically linked, stripped.
- Smoke test: `timeout 5 node main.ts` stayed up until the timeout
  (exit 124); with `PORT=8419`, `GET /` answered HTTP 401 with the
  expected `{"message":"Requires authentication", ...}` body.
- Grade-local on the exact v6 tree (worktree mode): **score 977** —
  feature 600, agent 200, perf 177, perf p50 266.130µs, working=true,
  reproduced=true. Feature/agent are at their maxima, matching every
  v4/v5 local run.
