# GLM-5.2 Core-Safe Matrix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the unsafe Phase 0 core matrix with paired default/high long-context tests, isolated cache A/B arms, and explicit incomplete-stream evidence without sending real requests.

**Architecture:** Keep the existing standard-library Python runner and add case metadata for prompt namespace and session behavior. `core-safe` excludes xhigh, while stream parsing records whether `[DONE]` and a consumable terminal result were observed. Cache arms use different prompt namespaces so one arm cannot warm another.

**Tech Stack:** Python 3 standard library, `unittest`, Git, OpenRouter-compatible Chat Completions protocol.

---

### Task 1: Specify the safe matrix

**Files:**
- Modify: `scripts/test_glm52_v021_phase0a.py`
- Modify: `scripts/glm52_v021_phase0a.py`

**Steps:**
1. Add failing tests asserting that `core-safe` contains paired `default` and `high` cases for 50/100/200/300 histories and 8192/16384/32768 output caps.
2. Assert that `core-safe` contains no `xhigh` case.
3. Assert that the cache experiment contains two requests for each of `shared`, `unique`, and `omitted` session modes.
4. Implement the minimal matrix and rerun the unit suite.

### Task 2: Isolate cache A/B variables

**Files:**
- Modify: `scripts/test_glm52_v021_phase0a.py`
- Modify: `scripts/glm52_v021_phase0a.py`

**Steps:**
1. Add tests proving requests within one cache arm have identical messages.
2. Add tests proving different arms have different prompt namespaces.
3. Add tests proving shared-session requests reuse one session ID, unique-session requests do not, and omitted-session requests contain no `session_id`.
4. Add `prompt_variant` and `session_mode` case metadata and implement request construction.

### Task 3: Capture incomplete stream evidence

**Files:**
- Modify: `scripts/test_glm52_v021_phase0a.py`
- Modify: `scripts/glm52_v021_phase0a.py`

**Steps:**
1. Add tests for `[DONE]`, SSE event counts, and valid terminal classification helpers.
2. Record `saw_done`, `sse_event_count`, `last_event_ms`, `stream_complete`, and `terminal_valid` in `summary.csv`/JSONL.
3. Count a request as successful only when HTTP is 2xx, the terminal is consumable, and a streamed response reached `[DONE]`.
4. Preserve raw SSE and transport errors for later forensic review.

### Task 4: Document and verify offline

**Files:**
- Modify: `docs/glm-5.2/v0.2.1-phase0a-report-template.md`
- Modify: `scripts/test_glm52_v021_phase0a.py`

**Steps:**
1. Document the corrected evidence language: cache existence is not session causality; incomplete xhigh is a symptom, not a proven token-exhaustion cause.
2. Add a Core-Safe and cache A/B reporting section.
3. Run `python scripts/test_glm52_v021_phase0a.py` and `python -m py_compile`.
4. Run `core-safe` in dry-run mode only and record case count and conservative worst-case cost.
5. Scan generated requests to confirm no `provider.order`, no xhigh, and no credential material.

### Task 5: Publish for Elon review

**Files:**
- Commit all files above.

**Steps:**
1. Run `git diff --check` and confirm the worktree contains only intended changes.
2. Commit with an auditable message.
3. Push `feat/glm-5.2-v1.1` using the repository Deploy Key.
4. Verify the GitHub branch SHA matches local HEAD.
5. Do not run OpenRouter, core-safe, Task 3, Task 0B, or any production deployment.
