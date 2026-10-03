# Proposal: concurrency and time as messages (actor style)

Status: draft, competing with `docs/proposals/concurrency-and-time.md` (the
"incumbent", `select`). One supporting spike commit on this branch
(`case: narrow abstract GraphQL handles at runtime`, §6.1).

Paths are `dang:` (this repo, after the spike) or `dagger:` (dagger/dagger).

## 1. Core idea and who finds it familiar

**Everything that can wake code is a message in a mailbox the engine owns,
and code waits in exactly one place: `receive`.** A tick from a schedule, an
agent settling, a human typing into the TUI, a foreman's reply, and the
completion of a build the code *posted* all arrive the same way, totally
ordered, in one queue. Dang needs no new syntax for this: `receive` is an
ordinary Dagger field returning an abstract `Message`, and dispatch is the
`case` type pattern Dang already has.

```dang
case (me.receive(after: "5m")) {
  t: Tick       => poll(t.tag)
  e: AgentEvent => settle(e.agent)
  m: Text       => obey(m)
  x: Timeout    => idle
  x: Message    => self
}
```

The second half of the idea: **code can be an actor.** A module function marked
`@actor` has the shape `(state, message) -> state`, and the engine calls it once
per message. A Process driven this way sits on the same roster as an LLM
`Agent`, uses the same mailbox, `send`, `notify`, `pause`, `stop` and trace,
and gets the same restore from telemetry.

This is not a new paradigm for Dagger. It extends the one Dagger already
chose: async-agents §2 calls "mailbox + blocking receive" "the minimal complete
kernel", and agent-messaging §3 states the rule "IDLE is the wait state, the
mailbox is the waker" (both `dagger:hack/designs/`). The agent loop already
*is* that kernel (`dagger:core/agent.go:1831`), and it is generic except for
`HasPending`/`Step` on an `*LLM` (`:1951`, `:1970`). This proposal swaps the
LLM out for code.

**Who finds it familiar:** Erlang/Elixir (`receive … after`,
`gen_server:handle_info/2`), Akka and Orleans users; anyone who has written a
reducer (Redux `(state, action) => state`, Elm `update`), which is exactly what
an `@actor` handler is; anyone who knows an event loop draining one queue; and
every `dagger agent` user, who already knows `send`, `notify`, IDLE/FAILED and
the roster. A Process is an agent whose brain is a function.

## 2. Surface

### 2.1 Dagger API (SDL sketch)

```graphql
extend type Query {
  """Mint a process (DoNotCache, pinned like LLM.spawn). With `state`, the
  engine drives it by calling the state's @actor function per message. Without
  it, the caller drives it with `receive`, and it stops when the minting call
  returns (it is linked to that call)."""
  process(name: String!, state: ID, handle: String): Process!
}

type Process {                      # same runtime table and verbs as Agent
  id: ProcessID!   name: String!   handle: String!
  state: AgentState!   error: String!
  "Behaviour form: the state after the last handled message (re-pinned, §3.6)."
  snapshot: ID
  send(message: String!, replyTo: String): ID!   # @expectedType(Message)
  "Deliver a Tick tagged `tag` once after `after`, then every `every`."
  schedule(tag: String!, after: String!, every: String): ID!   # @expectedType(Schedule)
  "Evaluate `field` (default sync) of the object `target` in the engine, then
  deliver Done or Failed tagged `tag`. Never blocks the caller."
  post(tag: String!, target: ID!, field: String! = "sync"): ID!   # @expectedType(Job)
  "Receive form only. Block until a message matching the filter is queued and
  dequeue it, or return a Timeout once `after` elapses or `until` passes."
  receive(kinds: [MessageKind!], tags: [String!], after: String, until: String): ID!  # @expectedType(Message)
  message(ref: String!): Message!          # pinned lookup of a received message
  messages(after: Int! = 0): [Message!]!   # audit read, never consumes
  notify(subscriber: AgentID, process: ProcessID, on: [AgentState!] = [IDLE, FAILED]): Void
  pause: Process!   resume: Process!   stop(kill: Boolean = false): Process!
  "Block until STOPPED or FAILED (Erlang: monitor). Both outcomes are terminal,
  so this cannot hang on the wrong one (agent-messaging §4.4)."
  join: AgentState!
}

enum MessageKind { TEXT TICK AGENT_EVENT DONE FAILED }
interface Message { ref: String!  seq: Int!  origin: AgentMessageOrigin! }
type Text       implements Message { text: String!  replyTo: String  reply(text: String!): ID! }
type Tick       implements Message { tag: String!  due: String!  missed: Int!  schedule: Schedule! }
type AgentEvent implements Message { agent: Agent  process: Process  state: AgentState!  reply: String!  error: String! }
type Done       implements Message { tag: String!  job: Job!  text: String!  value: JSON! }
type Failed     implements Message { tag: String!  job: Job!  error: String! }
type Timeout    implements Message { waited: String! }   # synthesized by receive, never queued
type Schedule { id: ID!  cancel: Void }
type Job      { id: ID!  tag: String!  cancel: Void }

extend type Agent {
  notify(subscriber: AgentID, process: ProcessID, on: [AgentState!] = [IDLE, FAILED]): Void
  schedule(tag: String!, after: String!, every: String): ID!  # Tick → event-origin prompt
  post(tag: String!, target: ID!, field: String! = "sync"): ID! # Done → event-origin prompt
}
directive @actor on FIELD_DEFINITION   # beside @agent, dagger:core/schema/module.go:183
```

### 2.2 Dang surface: nothing new

- `receive` is a field whose result is `ID! @expectedType(Message)`. Dang
  already promotes such IDs to lazy handles loaded by `node(id:)`
  (`dang:pkg/dang/eval.go:951`, `:843`), and `case` type patterns already
  narrow interfaces (`dang:pkg/dang/ast_patterns.go:229`). The spike makes the
  two meet: `case` resolves an abstract handle's `__typename` before matching
  (§6.1).
- `@actor` is a directive like `@agent` and `@check`. The Dang SDK maps it to a
  `withActor` selector (precedent: `dagger:core/sdk/dang/v2/helpers.go:844`).
- A handler can declare `me: Process!` and have it injected, the way `Agent!`
  is injected today (`AgentToContext`, `dagger:core/agent_context.go:23`;
  bound by the loop at `dagger:core/agent.go:1838`).

**Why `receive` needs no syntax when `select` does.** The incumbent (§2.6)
argues correctly that `select` cannot be a builtin. Its guards must stay
unevaluated so they can be raced, and its arms need bindings and bodies in the
caller's scope. `receive` has neither problem, because it races nothing: the
racing already happened in the engine at enqueue time, under one lock. What
reaches Dang is a single value, so the "arms" are patterns over that value,
which is `case`. Bindings, narrowing, `break`/`return` in bodies, union-widened
results and reachability errors all come from `Case` as it stands
(`dang:pkg/dang/dang.peg:1227`, `ast_patterns.go:52`). There is no contextual
keyword, so nothing is shadowed (incumbent §6.1 admits a breaking change), and
no tree-sitter, editor or LSP work.

Erlang's `after` clause becomes a type pattern too. `receive(after:)` returns a
`Timeout` message, so `t: Timeout =>` sits beside the other clauses. A timeout
is neither null nor an error.

## 3. Semantics

### 3.1 Atomicity and ordering

There is one queue per process, and enqueue assigns a sequence number under the
entry's lock, exactly as agent messages do (`msgSeq`, `dagger:core/agent.go:1465`).
`receive` dequeues under that same lock. Its wait is a Go `select` over the
entry's wake channel, an engine timer and `ctx.Done()`, the same shape as
`WaitSettled` (`:2465`–`:2481`). Every message is therefore either returned to
exactly one receiver or still queued. Messages from N workers, timers, humans
and jobs share one total order. Contrast incumbent §2.5: under `select`,
"which one wins is **not specified**".

### 3.2 Consumption: destructive, and safe here

Incumbent §2.4 rejects destructive receive because "a losing guard was
`mailbox.receive`… the message is lost." That hazard comes from *racing* a
receive against other guards. In this design nothing races a receive: it is the
only wait. The one remaining cancellation is the caller's own (Ctrl-C, or the
function being cancelled). For that case the engine pops a message only after
re-checking `ctx.Err()` under the lock, and records which receive got it.

**Replay of a cached ID.** `receive` is `DoNotCache`, and it returns the pinned
`process.message(ref:)` chain. That is the minted-and-pinned family `send` and
`spawn` already use (`dagger:core/schema/llm.go:277`–`:283`). Re-loading a
received message's ID re-addresses the same message and never receives again.
The spike keeps that pin: `case` reloads by `KnownID` (§6.1).

**Selective receive.** The `kinds`/`tags` filters give Erlang's save queue:
non-matching messages stay queued, in order, for a later `receive`. Dispatch
still happens in `case`. The filter only decides what may be consumed, so a
pattern list cannot drop a message it does not mention. Priority uses the
Erlang idiom, `receive(kinds: [TEXT], after: "0s")` first and then the general
receive.

### 3.3 Cancellation

- Cancelling `receive` dequeues nothing.
- Jobs run in the engine on the process's detached context, not the caller's.
  `Job.cancel` cancels one. Stopping the process cancels all of them (a link).
  In the receive form the process stops when the minting call returns, so no
  posted work outlives the function that started it. That is the incumbent's
  structured-concurrency guarantee, enforced by the engine.
- A posted `AgentMessage.response` or `Agent.wait` only observes, so cancelling
  it leaves the agent running (`WaitSettled` returns `context.Cause(ctx)`,
  `dagger:core/agent.go:2478`).

### 3.4 Failure

- **Errors in posted work are messages, not raises.** A failing job delivers
  `Failed { tag, error }`. The receiver chooses whether to retry, ignore or
  raise, so one bad probe cannot tear down an orchestrator. With
  `kinds: [DONE]` the receiver waits for the first *success* (example 5b).
- **A raising handler fails the process, not the caller.** The process goes
  FAILED and its message stays consumed-pending, the same handling agents get
  (`dagger:core/agent.go:1866`–`:1880`). `resume` retries that message.
- **Supervision follows directly.** `p.notify(process: supervisor, on:
  [FAILED])` delivers an `AgentEvent`, and the supervisor's handler decides
  whether to `resume`, re-spawn or escalate. Events never relaunch a STOPPED
  subscriber (`:1426`).

### 3.5 Typing

- `Message` is an interface, and `case` checks every pattern against it
  (`resolveInterfaceTypePattern`, `ast_patterns.go:281`). A misspelled kind
  fails to compile: `type Tik does not implement interface Message`.
- An interface operand is never exhaustive except through an interface
  catch-all (`moduleCoveredBy`, `ast_patterns.go:200`). A `case` without
  `x: Message =>` or `else` is therefore nullable. In the behaviour form the
  handler returns `Factory!`, so **the type checker forces every handler to
  handle every message**. A forgotten kind is a compile error, not a silently
  dropped message.
- The weak spot is posted results. `Done.value` is `JSON!` and `Done.text`
  renders the result as text. When the target was an object, the original
  handle is the typed result (`d: Done => build`, example 5c).

### 3.6 Fairness, lifetime and restore

- **Fairness.** Messages are FIFO by sequence number. Schedules fire on
  absolute times (`due = previous due + every`), so a slow handler causes no
  drift. Missed firings coalesce into one Tick with `missed: N`. In the receive
  form, `until:` gives absolute deadlines, so a chatty mailbox cannot keep
  postponing a timeout (the incumbent's §2.5 fix, adopted).
- **Lifetime.** The receive form is linked to its minting call. The behaviour
  form is session-scoped, like agent runtimes (busybees §3.2's detached
  sessions would lift both kinds at once).
- **Restore.** Each handled message commits a new state through the same
  `commitLast` → control-publication path agents use, and schedules are
  published like notify subscriptions (`installSubscriptionLocked`,
  `dagger:core/agent.go:918`). Restore is then `process(name:, state:
  <published state>, handle: <h>)`, the twin of `spawn(handle:)`
  (`llm.go:268`). The state is **re-pinned** at commit, not kept as the
  growing `…handle(message:)…handle(message:)` chain. Replaying that chain
  would re-run every handler's side effects: the 33-agent re-spawn incident
  busybees §2.1 cites. As for agents, pending queued messages are outside the
  restore guarantee.

### 3.7 Blocking inside an agent turn, and inside a handler

agent-messaging §3 says "an agent's turn never blocks on another agent's
progress". A handler invocation *is* a turn, so the same rule applies to it,
and an LLM tool call already is one. `Process.receive` is **refused** when
`CallerAgent` resolves (`dagger:core/agent_context.go:44`) and when called from
inside an `@actor` handler. The error teaches the fix: "a turn waits by ending:
`schedule`, `post` or `notify` and handle the message when it arrives." `post`
is what makes the refusal livable: it is the non-blocking form of *every*
blocking verb. `post(target: msg, field: "response")` replaces `msg.response`,
and `post(target: agent, field: "wait")` replaces `agent.wait`. The same
`post`/`schedule` on `Agent` give LLM agents async tools as a free side
benefit.

## 4. Layering

| Layer | Contents |
|---|---|
| **Language** | No grammar or type-system change. One evaluator fix, already spiked: `case` narrows abstract GraphQL handles (§6.1). It is generic GraphQL, useful for any API returning interface/union IDs. Optional lint: warn when the result of a field documented as consuming flows into a non-exhaustive `case`. |
| **Dang stdlib** | Nothing required. Durations are Go-syntax strings at the API, as in the incumbent's `schedule`. The incumbent's `Duration`/`Time` scalars would be welcome, but they are orthogonal. |
| **Dagger API** | `Process`, `Message` and its kinds, `schedule`, `post`, `receive`, `notify(process:)`, `@actor`, and a driver abstraction in `AgentRuntime`. |

**Other SDKs benefit fully, and equally.** This is the strongest structural
argument for the design. The factory could be written in Go: `switch m :=
msg.(type)` over `dag.Process(...).Receive(ctx)`, or an `@actor`-tagged method.
Python could use `match`, and TypeScript a `switch` on `__typename`. A
`select` in Dang gives non-Dang SDKs nothing. They have native select, but
nothing to select *on* for "agent settled" or "human said stop" except the
incumbent's `Mailbox`, which is this proposal's receive form without patterns,
kinds or `post`.

## 5. Worked examples

### 5a. The factory (behaviour form)

```dang
type Factory {
  # … workers, issues, ledger, paused as in busybees-as-modules §2.1 …

  """Entry point of `dagger call factory run`: start the actor, stay attached."""
  run(source: Workspace!): AgentState! @cache(policy: Never) {
    let p = process(name: "factory", state: withSource(source))
    p.schedule(tag: "poll", after: "0s", every: "5m")
    p.join                                   # returns when stopped or failed
  }

  """One message, one turn. Return the next state; the engine commits it."""
  handle(message: Message!, me: Process!): Factory! @actor {
    case (message) {
      t: Tick => {
        if (t.tag.hasPrefix("ci:")) { recheck(me, t.tag.trimPrefix("ci:")) } else { tick(me, full: true) }
      }
      e: AgentEvent => settle(e.agent).tick(me, full: false)   # cheap local pass
      m: Text => case (m.text) {
        "stop"   => { me.stop; self }
        "poll"   => tick(me, full: true)
        "status" => { m.reply(status); self }
        else     => { foreman.send(m.text); self }
      }
      f: Failed => { me.schedule(tag: f.tag, after: "1m"); self }   # retry probe
      x: Message => self                     # required: the handler returns Factory!
    }
  }

  tick(me: Process!, full: Boolean!): Factory! {
    let f = if (full) { reconcile(pollGitHub) } else { self }
    f.dispatchable.each { job =>
      let w = roleFor(job).spawn(name: job.key)
      w.notify(process: me)                  # IDLE/FAILED arrive as AgentEvent
      f = f.withWorker(job.key, w)
    }
    f.awaitingCI.each { pr => me.schedule(tag: "ci:" + pr, after: "10m") }
    f
  }
}
```

The handler holds no `busy` list (incumbent §5.1 needs one because a
level-triggered `w.wait` wins instantly), no `followUps` list (the engine holds
the timers, so they appear in the trace and survive restore), and no cursor.
A human types `stop` into the TUI's factory entry, and it arrives as a `Text`
with provenance (agent-messaging §4.1).

**The receive form uses the same handler**, called by a loop instead of the
engine. That suits a script, or a first version:

```dang
run(source: Workspace!): Void @cache(policy: Never) {
  let f = withSource(source)
  let me = process(name: "factory")
  me.schedule(tag: "poll", after: "0s", every: "5m")
  loop {
    f = f.handle(me.receive, me)
    if (me.state == AgentState.STOPPED) { break }
  }
  null
}
```

### 5b. Race two registries (first *success*, with a cap)

```dang
let me = process(name: "pull")
me.post(tag: "docker.io", target: container.from("docker.io/library/alpine:3.20"))
me.post(tag: "mirror", target: container.from("mirror.gcr.io/library/alpine:3.20"))
let registry = case (me.receive(kinds: [MessageKind.DONE], after: "5m")) {
  d: Done => d.tag
  x: Message => raise "no registry answered within 5m"
}
me.stop          # cancels the loser now rather than at function return
```

A pull that fails stays queued as `Failed` and does not win. The incumbent's
`select` lets the first *finisher* win, errors included (its §2.2 rule 3), so
first-success there needs `rescue` inside each guard.

### 5c. Time-box a build

```dang
let build = source.dockerBuild
let me = process(name: "build")
me.post(tag: "build", target: build)
let built = case (me.receive(after: "10m")) {
  d: Done => build                     # synced in the engine; the handle is typed
  f: Failed => raise f.error
  x: Message => raise "build took longer than 10m"
}
```

### 5d. First of N reviewers, capped at an hour

```dang
let me = process(name: "review-57")
reviewers.each { r =>
  me.post(tag: r.name, target: r.send("review PR 57"), field: "response")
}
let verdict = case (me.receive(kinds: [MessageKind.DONE], after: "1h")) {
  d: Done => d.text
  x: Message => "no review in time"
}
```

`r.send` returns its `AgentMessage` ID at once (`dagger:core/schema/agent.go:74`).
`post` turns "await its response" into a message, so a reviewer that FAILED
cannot win, and later answers are dropped when `me` stops.

## 6. Implementation sketch and sizing

### 6.1 Dang (spiked)

The commit `case: narrow abstract GraphQL handles at runtime` changes
`Case.Eval` (`ast_patterns.go:361`). When any clause is a type pattern and the
operand is a lazy `GraphQLValue` whose schema type is an interface or union, it
queries `__typename` once. A handle with a `KnownID` is then reloaded by ID as
the concrete type (`resolveAbstractGraphQLValue`), which keeps the pin.
Previously `case (favoriteNodeID) { u: User => … }` silently took `else`,
because `loadObjectFromID` labels the handle with the abstract name
(`eval.go:861`). There is a new assertion in `tests/test_expected_type.dang`,
and `go test ./pkg/... ./tests/` passes (909 tests).

Remaining Dang work: ½ day for the optional consuming-field lint (it reuses
`rescue_analysis.go`'s tail analysis), ½ day for docs (`docs/lit` control-flow
and GraphQL interop). Language total: **~1 day**.

### 6.2 Dagger

| Piece | Where | Effort |
|---|---|---|
| Driver abstraction: `last` becomes `AnyObjectResult`. An `llmDriver` keeps today's `withPrompt` + `Step`; a `codeDriver`'s drain selector is `handler(message:)` and is the whole turn (`drainMailbox` already calls `srv.Select(ctx, inst, &next, selector)` generically, `core/agent.go:1699`); an `externalDriver` never steps, and `receive` pops | `core/agent.go` | 3–4 d |
| Message kinds on `agentMessageRecord`, `Process.message(ref:)` pin, `notify(process:)` through `queueEventLocked` (`:950`) | `core/agent.go`, `core/schema/agent.go` | 2 d |
| Schedules: per-table timers, absolute due, coalescing, publication and restore | new `core/schedule.go` | 2 d |
| `post`/`Job`: load target ID, select field on the process context, convert to Done/Failed | `core/schema/process.go` | 2 d |
| `receive` with filters/after/until, refusals (`CallerAgent`, in-handler) | same | 1 d |
| `@actor`: directive, validation (precedent `validateAgentFunction`, `core/module.go:1430`), SDK selector, `Process!` injection | module + `core/sdk/dang` | 2 d |
| Roster/TUI: processes publish the same control records; render Tick/Done as compact events | `dagql/idtui`, agent control | 2 d |
| Integration tests (ordering, selective receive, link cancel, restore, refusal) | `core/integration` | 2–3 d |

Dagger total: **~3 weeks**, against the incumbent's ~1 week Dagger + 4–6 days
Dang. The design moves work out of the language and into the engine,
deliberately.

## 7. Honest weaknesses, and what the incumbent does better

1. **Racing plain expressions takes ceremony.** Example 5b is 7 lines; the
   incumbent's is 4. A pure Dang computation cannot be posted at all, and
   neither can a non-Dagger GraphQL API, since Dang also targets those. For
   them this design offers nothing. The incumbent's `select`/`sleep`/
   `timeout` work against any API, and offline.
2. **Posted work is stringly-typed.** `field: "response"` is a string and
   `Done.value` is `JSON!`. A `select` binding has an inferred type.
3. **Destructive receive needs care.** Pinning and the ctx re-check close the
   window, but a response lost on the wire after the pop is recoverable only
   through `messages(after:)`. A cursor log is trivially safe.
4. **More engine surface.** The factory's correctness rests on engine timers,
   jobs and a third driver kind. Bugs land in Go, not in a Dang script.
5. **State re-pinning in the behaviour form** is new machinery (§3.6). Large
   or secret-bearing states in telemetry need care.
6. **No local `sleep`.** Sleeping means minting a process. Erlang's
   `timer:sleep` is literally `receive after T`, but minting a process is
   heavier than a goroutine timer.

What I would adopt from the incumbent: `Duration`/`Time` scalars and the
laziness lint. Its `sleep`/`timeout` would also be fine as pure-Dang stdlib for
scripts. The two proposals compose: keep the incumbent's stdlib timers, and
replace its `Mailbox` + `select`-for-orchestration with `Process`. What I would
not adopt is `select` as syntax. Its own best factory loop (§5.1, closing
paragraph) collapses to one `box.next` arm once events are messages, which is
this design.

## 8. Open questions

1. **Naming and typing of the roster.** Should `Process` be a separate type,
   or should `Agent` become an interface with `Agent`/`Process`
   implementations? `notify(subscriber:, process:)` is the pragmatic shape
   until dagql grows interface-typed ID arguments.
2. **Typed results.** Is something like `Done.object: Node` plus a `::` cast
   enough? Or should `post` grow an `@expectedType`-style hint so Dang infers
   the result type?
3. **Should the receive form exist at all?** It is what `dagger call` scripts
   want and it eases migration, but it holds a request open and its loop is
   not restorable. The behaviour form could be the only blessed form for
   long-lived loops.
4. **Mailbox bounds.** Selective receive can grow a queue without limit
   (Erlang's classic leak). Should there be a cap with a FAILED transition, or
   a roster warning?
5. **Durable processes and schedules** across sessions (busybees §3.2). The
   behaviour form's committed state + published schedules are the natural
   persistence unit.
6. **Dang-side sugar** without Dang knowing Dagger. Should the Dagger Dang SDK
   ship a small `.dang` helper module, e.g. `within(after:) { lazyHandle }`?
   A block returns a lazy handle without executing it, so the helper can
   `post` it.
