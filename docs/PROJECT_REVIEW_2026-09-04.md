# Collomia project review — September 4, 2026

**Historical assessment, not current usage guidance.** The tracked follow-up
is [IMPROVEMENT_PLAN.md](IMPROVEMENT_PLAN.md). Several findings below have been
fixed, and the proposed bundled Work toolkit was withdrawn on September 6.
Current capabilities and verification behavior are documented in
[CAPABILITIES.md](CAPABILITIES.md) and [COMPLETION.md](COMPLETION.md).

Reviewed development tree at commit `f523696`. This is an assessment and proposed backlog, not an implementation or an orchestration graduation decision. No application code or existing tests were changed.

**My assessment:** Collomia has a substantial foundation for a capable agent: one governed tool path, explicit trust, durable sessions, recoverable publication, provider adapters, skills, MCP, and bounded execution. Work is a sensible separate task profile. The next investment should concentrate on getting the full benefit of models, completing real user tasks with fewer interventions, and producing useful artifacts. More orchestration machinery is a lower priority until those improvements are measured.

The current evidence supports a serious runtime, but does not establish SOTA agent performance. The distinction matters: a deterministic test can prove a tool is governed correctly while prescribing every decision the agent makes.

**Review scope and verification.** I read `ROADMAP.md` and `docs/ORCHESTRATION_STRATEGY.md` completely; reviewed the README, Work contract, beta limits, testing and automation contracts, and relevant provider documentation; and traced provider responses through the agent, completion controller, sessions, tools, and TUI. This was a focused architecture and product review, not an exhaustive security audit or a live model benchmark. I did not inspect personal credentials or make paid model calls.

Fresh tests passed for `internal/provider`, `internal/agent`, `internal/tools`, `internal/tui`, and `internal/session` with `go test -count=1`. The offline `internal/eval` suite also passed, taking about 101 seconds. The first sandboxed attempt could not bind a fixture HTTP port; the successful run used approved execution outside that sandbox. I did not run the whole repository's race suite or native Windows/Linux qualification.

Five additional review probes exposed the behaviors below. They used temporary Go overlays, leaving source and existing tests unchanged. The reasoning probe exercised the TUI handler; truncation exercised `Agent.Run`; the recovery and shell-mutation probes exercised controller observations; pagination used a real temporary file. These are reproducible gaps despite the existing suites passing.

| Priority | Recommendation | Why it comes first |
| --- | --- | --- |
| P1 | Complete reasoning display and provider state preservation | Explains the reported symptom and affects model/tool continuity. |
| P1 | Correct terminal outcomes and failure identity | Prevents incomplete work from being reported as complete. |
| P1 | Bind completion to actual artifacts and effects | Work often produces files through scripts and external tools. |
| P1 | Build real model task evaluations | Establishes whether any proposed improvement actually helps. |
| P1 | Ship an end-to-end Work toolkit | Makes documents, spreadsheets, and research practical daily tasks. |
| P1 | Strengthen long-task context and recovery | Reduces repeated work and dependence on the user. |
| P2 | Add tool discovery and governed read concurrency | Improves scale and latency without requiring larger agent teams. |
| P2 | Make scoped autonomy and delayed continuation explicit | Supports longer work under authority the user has actually granted. |
| P2 | Simplify architecture documentation and module boundaries | Reduces drift and makes future capability changes easier to maintain. |

**1. Fix thinking as an end-to-end capability.**

The user's observation is real. [The agent emits reasoning events](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/agent.go#L504), but [the TUI handler](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/tui/model.go#L787) handles text, tool events, and warnings without a `KindReasoningDelta` case. Injecting a reasoning event leaves it absent from the transcript. The JSONL event contract already has `reasoning.delta`; the display is the missing consumer.

There are also upstream gaps:

- [Anthropic request construction](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/provider/anthropic.go#L124) maps the setting to `output_config.effort`; it does not send a thinking mode or display setting. Effort alone is not a universal instruction to enable or show thinking. Current Claude documentation distinguishes thinking configuration from effort, and some newer models omit readable thinking by default. Complete unmodified thinking blocks and signatures must accompany tool results. [Claude thinking documentation](https://platform.claude.com/docs/en/build-with-claude/thinking).
- [The provider factory](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/provider/factory.go#L23) routes OpenAI and Azure OpenAI families through Chat Completions. Responses is exposed through the Bedrock Mantle type. [That adapter](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/provider/responses.go#L48) sends effort but does not request a reasoning summary. OpenAI documents explicit summary opt-in and preservation of reasoning output items for stateless continuation. [OpenAI reasoning documentation](https://developers.openai.com/api/docs/guides/reasoning).
- [The shared response/message model](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/provider/types.go#L111) cannot carry signed thinking or opaque reasoning items, and [assistant history](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/agent.go#L589) retains only text and tool calls. Streaming reasoning to an event is not the same as preserving provider state. The capability registry already acknowledges partial support.

Implement a collapsible, bounded “Thinking summary” block, separate from final answers and ordinary progress. Show elapsed activity when no readable summary is provided. Preserve ordered content blocks and provider-bound opaque state through tools, persistence, and resume; keep signatures out of display text and prevent incompatible replay after a provider switch. Add explicit API-route selection rather than asking users to select the wrong provider family to reach Responses.

Expose reasoning enablement, supported effort levels, readable-summary availability, and state preservation separately in diagnostics. Display the effective negotiated setting, including fallback, rather than only the requested setting. Test thinking → tool call → tool result → answer, streaming and synchronous fallback, and close/reopen. Do not promise access to a model's complete private reasoning or infer that absent text means absent reasoning.

**2. Make terminal results trustworthy without making the model repair excessive bookkeeping.**

Two concrete findings:

- A fake provider response with `Stop: "length"` or `Stop: "max_tokens"` and incomplete text is accepted as done by `Agent.Run` after one request. [Completion branches on tool absence and the controller](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/agent.go#L684), without assessing the provider stop reason. Normalize terminal reasons into completed, requires-tools, truncated, refused, and failed; admit completion only for a compatible terminal reason. Bounded continuation may be appropriate, but must not replay tools or bypass the remaining budget.
- [Failure recovery compares only tool names](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/completion.go#L271). A failed read of required.csv is cleared by a successful read of README.md. Likewise, a successful command can clear failures from different commands. The probe confirmed the failure list becomes empty. Store the operation and subject identity, and resolve a specific failure through an exact retry or an explicit alternative backed by a successful receipt.

I would replace the increasingly elaborate plan-mediated recovery protocol with a small typed completion interface: current gaps, accepted receipts, outstanding subjects, and explicit failure resolution. Keep the plan about the work. The runtime can automatically recognize exact retries; only semantic alternatives need model judgment. Harmless exploratory misses should not require creating an otherwise unnecessary plan, while failed required operations and ambiguous mutations remain unresolved.

Exit criteria: unrelated successes cannot erase failures; truncation cannot yield `done`; a recovered task finishes without repeated metadata-only provider calls; permission denials remain distinguishable from failures after execution began.

**3. Track effects separately from permission risk, and validate the intended deliverables.**

[Shell actions are `RiskExecute`](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/tools/command.go#L140), while [Standard dirtiness tracks `RiskWrite`](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/completion.go#L163). A controller probe of write report → validate report → shell mutation still reports done. This is the documented command-manifest limitation, with a particularly important consequence for Work: scripts are a normal way to create or modify Office files and data.

Add an effect contract independent of permission classification: read-only, local mutation, external mutation, or unknown; observed changed paths; operation identity; and any verification/postcondition receipt. For declared deliverables, compare their final digests with receipts at completion, regardless of which tool wrote them. Observe or conservatively reconcile unknown command effects. Do not turn every harmless shell read into a mandatory test cycle.

Introduce an optional task brief for substantive work: requested outputs, acceptance criteria, input sources, constraints, and declared deliverable paths. Separate deliverables from scratch scripts, caches, and previews so an auxiliary script does not force the agent into the same acceptance workflow as the report the user requested. Keep direct Q&A free of that ceremony.

For external actions, persist intent and operation ID before execution, retain the returned receipt, and use a service-specific read-back after an uncertain result. An HTTP success alone is not a universal business postcondition. These are new capability contracts, not permissions inferred from a plan.

**4. Establish real agent-quality evaluations before claiming SOTA.**

[The evaluation provider is scripted](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/eval/eval_test.go#L1). This is excellent for reproducible runtime tests. It cannot measure whether a real model selects the right tools, notices an error, asks a useful question, or produces a good document. The orchestration strategy explicitly limits its simulated timing claims.

Create an opt-in suite of roughly 30–50 representative tasks, initially drawn from real problems you encounter:

- Code: unfamiliar-repository diagnosis, multi-file repair, preserving an unrelated user edit, and recovering from a failed test.
- Work: a cited comparison, a CSV reconciliation, an XLSX workbook with checked formulas, a DOCX memo, a presentation whose pages are rendered, and revising an existing deliverable.
- Autonomy: ambiguous requirements, a failed-but-recoverable tool, a long task spanning compaction, cancellation/resume, and an external action whose response is lost after submission.

Use held-out acceptance checks, reproducible data assertions, and human or blinded rubric review where quality is subjective. A model-written test that merely agrees with its own implementation is insufficient. Run multiple trials with the same model and comparable budgets when evaluating harness changes; then compare providers separately.

Track accepted outcomes, false done, false blocked, unnecessary questions, user interventions, repeated work, total elapsed time, and cost per accepted task. Retain local inspectable traces with opt-in sharing. Judge greater autonomy by completed work per user intervention, not by tool calls, tokens, or number of agents.

**5. Give Work a complete input → creation → inspection → revision workflow.**

Work currently generalizes prompts and evidence more than tooling. [Built-ins](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/tools/builtin.go#L48) remain largely oriented around text/code, with artifact validation added. Skills and MCP can extend this, but a new user still needs to assemble the useful pieces.

Ship a small maintained Work bundle: document extraction, spreadsheet operations, document/presentation generation, rendering, and image inspection. Dependencies should be discovered or installed through an explicit setup flow with compatible versions. Prefer tested format libraries and bundled scripts over reproducing file-format logic in the core agent.

[PDF validation](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/tools/artifact.go#L233) checks header/end markers; it does not parse or render a document. XLSX currently falls through to generic binary validation. Make receipts say exactly what was established: parse/open success, formula or reconciliation checks, rendered-page inspection, and separate content/source review. A digest proves identity, not quality.

There is also an immediate input bug: [read_file limits the underlying stream before applying line offset](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/tools/files.go#L68). A real 1.1 MB test file returns `(no lines)` when asked for a known line beyond the first MiB, despite the tool recommending chunked reads. Fix pagination and return explicit continuation/truncation metadata. This matters for data exports and long documents as well as source files.

For research, extend the existing search/fetch path with a source registry carrying URL, retrieval time, title, extracted passage, and locator. Keep cited evidence recoverable after compaction. Add an optional browser path for JavaScript/authenticated pages and optional alternative search backends; preserve domain policy and external-data framing. The existing built-in public-web address boundary should remain intact.

Exit criterion: a fresh Work installation can inspect supplied inputs, create a useful DOCX/XLSX/PPTX/PDF artifact, inspect its content and appearance, revise it, and hand it off without an improvised installation/debugging detour.

**6. Strengthen long-task context and recovery before adding persistent cross-project memory.**

[Compaction](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/agent.go#L2752) summarizes older messages and keeps a recent tail. Pinned state and verbatim failure retention are good foundations. Add a bounded task record containing the original objective, user constraints and corrections, decisions, artifact/source references, accepted evidence, unresolved questions, and next action. Test that the next model decision still respects those facts after repeated compactions.

Provide model-accessible search/read of earlier session evidence, not just oversized output by a remembered artifact ID. Summaries should point to recoverable observations. For opt-in workspace memory, retain provenance, timestamps, explicit user corrections, and edit/delete controls. Keep permissions outside model-authored memory. Broad semantic memory across unrelated repositories is explicitly deferred today; it should require a separate decision, not arrive as a side effect of this work.

Persist Standard task completion state and recoverable file checkpoints across restart. The current controller is created per run, and ordinary coupled restore has a documented process-local limit. A user should be able to resume a partially complete report or code change with its evidence and pending obligations intact. Use bounded content-addressed snapshots or journals, without requiring visible Git commits in Work folders. Reconcile uncertain external effects rather than replaying them.

**7. Improve ordinary tool use before expanding multi-agent autonomy.**

[All available tool definitions are supplied on each request](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/agent.go#L1217), and [ordinary tool calls execute serially](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/agent/agent.go#L713). With several MCP servers, tool selection and schema overhead can dominate context.

Add catalog search with lazy schema loading, bounded tool results, and stable invocation IDs across start/output/result events. Distinguish discovering a tool from authorizing it. Add bounded concurrency only for explicitly replay-safe, independent read tools; preserve deterministic result association and serial mutation ordering. Benchmark this against existing serial execution before adding more workers.

Read-only delegates already exist; make them useful for Work investigations. The current planning tool allowlist excludes MCP tools by name, so a researcher cannot automatically inherit connected knowledge sources. Introduce locally trusted read-only capability declarations and resource policy, rather than allowing server-authored annotations to lower risk.

Do not make Orchestrated Goal the default or automatically integrate writer candidates. Both conflict with ratified decisions. Work graph execution needs a new state/evidence contract. Start any future extension with read-only source-bound work; non-Git writers and reviewed publication are a separate milestone.

**8. Make useful autonomy a scoped, user-owned mandate.**

The current runtime can execute and recover, but background processes and one-shot JSONL are not a durable service that wakes later. A future task mandate could specify workspace/output scope, approved tools and destinations, spend/time limits, and when to stop or ask. Keep it distinct from graph approval, and visible and revocable by the user.

Under that mandate, the agent can make reversible task-local choices, repair ordinary errors, and continue productive work without asking repeatedly. Publication, new destinations, broader scope, and uncertain external mutations must still follow their applicable authorization paths. Approval should concern the concrete artifact or operation, not a vague promise to prepare it later.

Then add a durable task queue and event/timer wakeups with leases, cancellation, deduplication, and explicit waiting-for-input states. A restart can safely resume approved observation work; an uncertain mutation needs reconciliation. Because headless Orchestrated activation is deliberately absent, this requires a fresh product decision and an automation contract before implementation.

Also improve the base execution prompt: state that action requests should be carried through to a usable result, encourage reasonable reversible assumptions, and require concise progress on long tasks. The existing prompt is heavily weighted toward rules and completion repair. Measure any new guidance on false completion and unnecessary questions rather than assuming a longer prompt is better.

**9. Reduce maintenance and context overhead.**

The roadmap and orchestration strategy together contain 5,615 lines despite a separate history file. Current decisions are interleaved with superseded milestone narratives. One example of drift: the roadmap still has an unchecked OG-3B parent checkbox although its children and overall program are complete. A contributor required to read both documents pays a large context cost before changing anything.

Keep a short current roadmap, a concise authority/state contract, and a brief handoff with the current decision and next gate. Move completed narratives to history/ADRs and link them. Keep documentation guards, but check meaningful contracts rather than requiring repeated prose everywhere.

Large files also mix responsibilities: `agent.go` is about 2,938 lines, `app.go` 2,087, `model.go` 1,931, and `goalgraph/graph.go` 4,936. Extract cohesive provider-turn handling, completion/effects, context management, delegation, and UI projections incrementally as those areas change. Retain one permission/execution path; a wholesale rewrite would risk losing the project's strongest properties.

Revisit instruction precedence separately: [global instructions explicitly defer to project instructions](https://github.com/robert-mcdermott/collomia/blob/f523696/internal/skills/skills.go#L398). Prefer explicit user requirements over conflicting project defaults, and distinguish trusted project workflow guidance from instructions embedded in ordinary retrieved data. Nested instruction discovery should have provenance and a clear scope, not just concatenate more text into the system prompt.

**Suggested next three increments:**

1. Repair the confirmed display, stop-reason, failure-identity, and pagination defects; design and begin provider-state round trips. Add the missing regressions.
2. Deliver one polished Work vertical slice—such as data inputs → reconciled workbook → cited memo—with final digest checks, rendering, and real-model acceptance evaluations. Broaden only after it works reliably.
3. Add durable task context/checkpoints, tool discovery, and measured safe read concurrency. Use intervention and completion metrics to decide whether delayed execution or further orchestration is the next constraint.

My strongest recommendation is to measure and improve complete user outcomes now. The project already has enough execution machinery to expose the next problems through real work.
