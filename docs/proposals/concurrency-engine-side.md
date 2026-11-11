# Proposal: concurrency and time as engine-side awaitables

Status: draft, with a spike on this branch (§6.3). It competes with
`docs/proposals/concurrency-and-time.md` (the **incumbent**, `dang` staff
branch at 88fcac3): a Dang `select` expression, `sleep`/`timeout`/`.race` in
the stdlib, and a `Mailbox` in the Dagger API.

`dagger:` paths are in the dagger repo (vito/dagger at 61744bd, which has the
Agent API); bare paths are in this repo.

## 1. Core idea and who finds it familiar

**Put the race where the state lives.** Everything the factory waits on is
already engine state: agent lifecycles (`transitionLocked` broadcasts every
change, `dagger:core/agent.go:1283`), message answers, builds, services. Only
the engine can observe them atomically, own the clocks, show a wait in the
trace, refuse a deadlocking wait, and serve one primitive to the Go, Python
and TypeScript SDKs. So the engine gets the primitives and Dang gets no new
syntax:

1. **Awaitables are names for facts.** An awaitable's `sync` blocks until a
   fact exists: *19:05 has passed*, *the mailbox has an event after #41*,
   *`dev/42` has settled a 3rd time*, *this container is built*. Naming a
   fact never blocks; once true it stays true, so it is cacheable and losing
   a race over it loses nothing. The interface exists: `Syncer`
   (`dagger:core/schema/coreinterfaces.go:12`), already implemented by
   `Container`, `Directory`, `Service`… (`dagger:docs/docs-graphql/schema.graphqls:1067,6578`).
2. **`Query.awaitAny(of: [Syncer], timeout, mode): Int`** races them in one
   resolver and returns the winner's index (null on timeout). Losers are
   cancelled and awaited before it returns.
3. **A `Mailbox`** is the edge-triggered log that agent settles
   (`notify(mailbox:)`), schedules, watched awaitables and human/agent sends
   all append to, in one total order. The run loop is one blocking read:
   `box.next(after: cursor)`.

Familiar to users of **epoll/kqueue** (the kernel owns readiness; userland
asks "which one?"), **Temporal** (`Selector`, server-side durable timers),
**Kubernetes watches / Kafka** (a client-held resource version or offset),
**Erlang** (one mailbox per process), and Go's `context` cancellation.

## 2. Surface

### 2.1 Dagger schema (all `Experimental`, v1 view)

```graphql
scalar Duration     # Go syntax: "300ms", "5m", "1h30m"; Dang coerces literals
scalar Timestamp    # RFC 3339, UTC

extend type Query {
  now: Timestamp!                                                 # DoNotCache
  "Mint a deadline; `after` is resolved now and the ID pinned to the instant."
  timer(after: Duration, at: Timestamp): ID! @expectedType(name: "Timer")
  sleep(duration: Duration, until: Timestamp): Void              # DoNotCache
  "Mint a session mailbox. Like spawn, the handle is the capability."
  mailbox(name: String! = ""): ID! @expectedType(name: "Mailbox")
  "Index of the first awaitable to sync, or null if `timeout` elapses first."
  awaitAny(of: [ID!]! @expectedType(name: "Syncer"), timeout: Duration,
           mode: AwaitMode = FIRST_SETTLED): Int                 # DoNotCache
  awaitAll(of: [ID!]! @expectedType(name: "Syncer"), timeout: Duration): Boolean!
}
enum AwaitMode { FIRST_SETTLED FIRST_SUCCESS }

type Timer implements Node & Syncer {
  id: ID!  at: Timestamp!
  add(duration: Duration!): Timer!        # pure: the next deadline
  sync: ID!                               # blocks until `at`
}

type Mailbox implements Node {
  id: ID!  name: String!
  "Append a MESSAGE; origin resolved at enqueue, as Agent.send does. Returns its seq."
  send(message: String!, replyTo: String): Int!
  "Append a TICK at `at`/`after`, then every `every` (fixed rate, skips missed ticks)."
  schedule(label: String!, message: String! = "", after: Duration, at: Timestamp,
           every: Duration): ID! @expectedType(name: "Schedule")
  "Append FIRED (or FAILED) once, when the awaitable syncs."
  watch(awaitable: ID! @expectedType(name: "Syncer"), label: String! = ""): ID! @expectedType(name: "Mailbox")
  "The first event with seq > after. Naming it never blocks; its fields do."
  next(after: Int! = 0): MailboxEvent!
  events(after: Int! = 0): [MailboxEvent!]!                      # non-blocking
}
type MailboxEvent implements Node & Syncer {
  id: ID!  seq: Int!  kind: MailboxEventKind!  label: String!  text: String!
  at: Timestamp!  origin: LLMMessageOrigin  agent: ID @expectedType(name: "Agent")
  sync: ID!
}
enum MailboxEventKind { MESSAGE TICK SETTLED FAILED FIRED }
type Schedule implements Node { id: ID!  cancel: Void }

extend type Agent {
  "Widened: deliver lifecycle events to an agent OR a mailbox."
  notify(subscriber: ID @expectedType(name: "Agent"), mailbox: ID @expectedType(name: "Mailbox"),
         label: String! = "", on: [AgentState!] = [IDLE, FAILED]): ID!
  settles: Int!                                                  # DoNotCache
  "Names the (after+1)-th settle: edge-triggered, never re-fires an old one."
  settled(after: Int!): AgentSettlement!
}
type AgentSettlement implements Node & Syncer {
  id: ID!  count: Int!  state: AgentState!  reply: String  error: String  sync: ID!
}
extend type AgentMessage { sync: ID! }    # joins Syncer: blocks until answered
```

That is the whole change. `Agent.wait`, `AgentMessage.response` and every
existing `sync` keep their meaning.

### 2.2 Dang: no new syntax

`[ID!]! @expectedType(name: "Syncer")` already maps to a plain `[Syncer!]!`
list of objects (`pkg/dang/expected_type_test.go:70`); objects marshal to
their IDs when passed (`pkg/dang/eval.go:1093`, `:330`); `ID!` results come
back as handles loaded through `node(id:)` (`pkg/dang/eval.go:843`); scalar
fields execute immediately, so imperative verbs run exactly once
(`pkg/dang/eval.go:253`). Programs dispatch with `==`/`if` on the index or
`case` value patterns on `ev.kind` (string literals coerce to enums).

## 3. Semantics

### 3.1 Atomicity: nothing left to make atomic

The incumbent's central rule (its §2.4) is that a race over remote calls is
not atomic, so "losing arms must lose nothing" — a rule it can only
*document*, because "Dang cannot know which GraphQL fields have side
effects". Here the rule is structural:

- Every awaitable is a **monotone fact**. Observing consumes nothing; no
  awaitable dequeues, so no race can drop a message.
- The mailbox **serializes** sends, ticks, settles and watches into one log
  under one lock. Two things "at once" get two seqs; the program never races
  its sources, it reads the next one.
- **Naming never blocks; reading does.** `box.next(after: 41)` returns a
  handle immediately (Dagger's laziness contract: `container.from` does not
  pull until `sync`). That is what lets `awaitAny([box.next(after: c), …])`
  compute its argument IDs without blocking (spike:
  `tests/awaitserver/resolvers.go:45`).

### 3.2 Level vs. edge: no "already-IDLE wins forever" spin

`Agent.wait` is level-triggered (`dagger:core/agent.go:2457`); racing it in
a loop spins on an idle worker, which the incumbent works around by
filtering `busy` before each `select`. Here awaitables are **indexed by
occurrence** — `settled(after: n)`, `next(after: seq)`, `timer(at: t)` — so
a loop that advances its index cannot spin, and one that does not shows a
stale index in its own code. `Agent.wait` is deliberately not a `Syncer`.

The mailbox inherits the engine's edge bookkeeping: `queueEventsLocked`
fans out transitions only (`sub.last`, `dagger:core/agent.go:950-980`), an
already-reached state fires once at subscribe time (`:936`), and a restored
agent does not re-announce history (`:913`). `notify(mailbox:)` is a second
sink in `deliverEvent` (`:1049`).

### 3.3 Cancellation

The race runs in one resolver under a child context; on a winner or
timeout it cancels the rest and **waits for them to unwind** — the contract
of Dang's own `evalParallel` (`pkg/dang/eval.go:1661`), now enforced for
every SDK. The spike asserts the loser has unwound when `awaitAny` returns
(`raceBuilds`; `tests/awaitserver/engine.go:235`). Cancelling the caller
aborts its HTTP request to the nested-client server
(`dagger:core/sdk/dang/shared/shared.go:112`), cancelling the resolver;
observer waits return `context.Cause(ctx)` and change nothing
(`dagger:core/agent.go:2477`). A losing `Container.sync` is cancelled unless
another caller shares it through dagql's singleflight — the same answer and
open question as the incumbent's §2.5.

### 3.4 Failure and typing

- `FIRST_SETTLED`: a failing winner raises from `awaitAny` (Dang `rescue`s a
  `GraphQLError`) — `{{ }}`'s fail-fast rule.
- `FIRST_SUCCESS`: failures are skipped; it raises only when all failed.
  "First registry that works" — which `select` expresses only by wrapping
  each guard in `rescue` and re-racing.
- A timeout returns **null** (expected absence, not an error). Without a
  timeout the result is still typed `Int`: GraphQL nullability cannot
  depend on an argument (§8.2).
- The result is an index. The payload is read from the handle the caller
  already holds; facts are cached once resolved, so that read never waits
  again (spike: `first.text` after the race).

### 3.5 Fairness

Before racing, the resolver checks each observer's fact non-blockingly **in
index order**; the lowest already-true index wins
(`tests/awaitserver/engine.go:185`; spike asserts `awaitAny([timer(10s),
first, timer(0s)]) == 1`). Only genuinely concurrent completions are
ordered by arrival. The incumbent leaves ready-at-once order unspecified
(its §2.5). In the mailbox loop starvation cannot happen at all: a tick is
an event *in* the log, between messages, not an arm a relative sleep keeps
re-arming.

### 3.6 Cache policy, identity, capabilities

| Field | Policy | Why |
|---|---|---|
| `awaitAny`, `awaitAll`, `sleep`, `now`, `settles`, `events` | DoNotCache | timing-dependent |
| `mailbox`, `timer(after:)`, `schedule`, `watch`, `send`, `notify` | DoNotCache, return `ID!` | imperative verbs (`agentSelfID`, `dagger:core/schema/agent.go:203`) |
| `timer(at:)`, `next(after:)`, `settled(after:)`, their fields and `sync` | **cached** | a resolved fact never changes; a cancelled wait is an error, never cached |

Mints re-exec through a cached lookup so the ID is an honest, replayable
chain, as `send` and `spawn` do (`dagger:core/schema/agent.go:305-325`,
`dagger:core/schema/llm.go:261`): `timer(after: "5m")` returns an ID that
*is* `timer(at: "…19:05Z")` (spike: `tests/awaitserver/resolvers.go:112`).
Mailboxes descend from a per-session mint, so cached `next(after:)` results
cannot leak across sessions — `Agent.message`'s argument
(`dagger:core/schema/agent.go:87-96`). IDs are capabilities: only holders
of a mailbox ID can send, schedule or subscribe into it; `notify` needs
both handles; a foreman receives the box in its tool object.

### 3.7 Agent turns may not block — enforced, in every SDK

agent-messaging §3: "An agent's turn never blocks on another agent's
progress." The incumbent can only warn from Dang (its §4.3, "advisory").
Here every wait is an engine resolver that knows its caller
(`core.CallerAgent`, agent-messaging §10 step 1): `awaitAny` over agent
facts registers waits-for edges through `beginAgentWait`
(`dagger:core/agent.go:372`) and is refused on a cycle; `Mailbox.next` and
long `sleep`s from inside a turn are refused with a teaching error ("an
agent waits by ending its turn; schedule a message instead") — whether the
tool was written in Dang, Go or Python.

### 3.8 Lifetime, restore, trace

- **Scope.** Mailboxes, schedules and watches live in the session runtime
  table beside `AgentRuntimes` (`dagger:core/schema/agent.go:195`) and end
  with it. Timers are pure values.
- **An in-flight await is re-issued, not resumed.** A dead session takes
  its requests with it, and restore deliberately does not continue
  interrupted execution (`dagger:hack/designs/trace-native-agent-resume.md:378-388`).
  But every await names absolute facts — `timer(at:)`, `next(after: seq)`,
  `settled(after: n)` — so a restarted `run` with a persisted cursor waits
  on exactly the same facts, and an overdue deadline fires once,
  immediately. The incumbent's `sleep(until: nextPoll)` keeps its deadline
  only in interpreter memory. The append-only mailbox is also the natural
  durable unit for busybees §3.2 (log + cursor is what Kafka persists).
- **Trace.** Every await is a resolver span carrying its arguments (which
  recipes, deadline, cursor); losers are cancelled child spans; events link
  to the span that produced them. One attribute (`dagger.io/await`) lets the
  TUI draw a wait as idle rather than busy. The incumbent needs a new Dang
  host hook for any of this (its §6.5).

## 4. Layering

| Piece | Layer |
|---|---|
| `Syncer` as the awaitable interface, `awaitAny`/`awaitAll` (race, cancellation, fairness, refusal, trace) | Dagger API |
| `Timer`, `sleep`, `now`, `Duration`/`Timestamp` | Dagger API |
| `Mailbox`, `schedule`, `watch`, `notify(mailbox:)`, `settled(after:)` | Dagger API |
| `loop`, `case`, `if` over the results | Dang, unchanged |
| `interface X implements Y` kept by the SDL loader | Dang bug fix (ae79a04) |

**Every SDK benefits identically** — a genuine advantage. The factory in Go:

```go
func (f *Factory) Run(ctx context.Context, source *dagger.Workspace) error {
	box, err := dag.Mailbox(ctx, dagger.MailboxOpts{Name: "factory"})
	if err != nil {
		return err
	}
	if _, err := box.Schedule(ctx, "poll", dagger.MailboxScheduleOpts{After: "0s", Every: "5m"}); err != nil {
		return err
	}
	for cursor := 0; ; {
		ev := box.Next(dagger.MailboxNextOpts{After: cursor})
		if cursor, err = ev.Seq(ctx); err != nil { // blocks; later reads hit the cached fact
			return err
		}
		kind, _ := ev.Kind(ctx)
		label, _ := ev.Label(ctx)
		text, _ := ev.Text(ctx)
		switch {
		case kind == dagger.MailboxEventKindMessage && text == "stop":
			return nil
		case kind == dagger.MailboxEventKindTick && label == "poll":
			err = f.tick(ctx, source, box, true)
		case kind == dagger.MailboxEventKindSettled:
			err = f.settle(ctx, label, source, box)
		case kind == dagger.MailboxEventKindMessage:
			_, err = f.Foreman.Send(ctx, text)
		}
		if err != nil {
			return err
		}
	}
}
```

No SDK can today express "wake on the first of a settle, a message or a
clock" without goroutines polling `Agent.state`. A Dang-only `select` does
nothing for them.

## 5. Worked examples

### (a) The factory run loop

```dang
type Factory {
  # … state as in busybees-as-modules §2.1; tick(…, inbox:) subscribes each
  # agent it spawns with worker.notify(mailbox: inbox, label: key) …

  run(source: Workspace!): Void @cache(policy: Never) {
    let f = self
    let box = mailbox(name: "factory")        # ID! → a Mailbox handle, minted once
    box.schedule(label: "poll", after: "0s", every: "5m")
    let cursor = 0

    loop {
      # One blocking read: ticks, settles, failures, human and foreman
      # messages arrive serialized, in order, none lost.
      let ev = box.next(after: cursor).{{ seq, kind, label, text }}
      cursor = ev.seq

      case (ev.kind) {
        "TICK" => case (ev.label) {
          "poll" => { f = f.tick(source, full: true, inbox: box) }
          "recheck" => { f = f.recheck(issue: ev.text, inbox: box) }
        }
        "SETTLED" => { f = f.settle(ev.label).tick(source, full: false, inbox: box) }
        "FAILED" => { f = f.failed(ev.label, ev.text) }
        "MESSAGE" => case (ev.text) {
          "stop" => break
          "poll" => { f = f.tick(source, full: true, inbox: box) }
          else => { f.foreman.send(ev.text) }
        }
      }

      # follow-ups ("re-check CI in 10m") become engine timers, not Dang state
      f.takeFollowUps.each { u =>
        box.schedule(label: "recheck", message: toString(u.issue), after: u.after)
      }
      f = f.clearFollowUps
    }
    null
  }
}
```

The spike runs this loop's skeleton (a 20ms `poll` schedule, a one-shot
`stop`, a pre-sent message) against the fake engine (`factoryLoop`).

### (b) Race two registries

```dang
let docker = container.from("docker.io/library/alpine:3.20")
let mirror = container.from("mirror.gcr.io/library/alpine:3.20")
# a registry that errors cannot win; the slower pull is cancelled
let base = if (awaitAny([docker, mirror], mode: FIRST_SUCCESS) == 0) { docker } else { mirror }
```

### (c) Time-box a build

```dang
let built = source.dockerBuild
if (awaitAny([built], timeout: "10m") == null) {
  raise "build took longer than 10m"          # the build was cancelled
}
built                                         # already evaluated: a cache hit
```

No laziness lint needed (incumbent §2.3: a guard ending in a lazy
`Container` "finishes immediately"): the engine knows awaiting a
`Container` means `sync`, so the wrong thing cannot be raced.

### (d) First of N reviewers, 1h cap

```dang
let asks = reviewers.map { r => r.send("review PR 57") }   # [AgentMessage!]!, enqueued now
let w = awaitAny(asks, timeout: "1h")
let verdict = if (w != null) { asks[w].response } else { "no review in time" }
```

`AgentMessage.sync` blocks until answered, so `.response` then returns at
once. Losing reviewers keep working (their messages are facts, not
cancelled guards); `pause`/`stop` them if wanted. Kinds mix freely:
`awaitAny([build, timer(after: "10m")])` (spike: `deadline`).

## 6. Implementation sketch and sizing

### 6.1 dagger (~1.5–2 weeks)

| Piece | Where | LOC |
|---|---|---|
| `awaitAny`/`awaitAll`: load IDs, readiness pre-pass, race `srv.Select(…, "sync")` under a child ctx, cancel + wait | `core/await.go`, `core/schema/await.go` | ~250 |
| `Timer`, `timer`/`sleep`/`now`, scalars, pin `after`→`at` | `core/schema/time.go` | ~150 |
| `Mailbox` (log + changed channel like `stateChanged`), `next`/`events`/`schedule`/`watch` | `core/mailbox.go`, session runtime table | ~450 |
| `notify(mailbox:, label:)`: second sink in `deliverEvent` | `dagger:core/agent.go:886-1064` | ~100 |
| settle counter in `transitionLocked`; `settles`, `settled(after:)` | `core/agent.go`, schema | ~120 |
| `AgentMessage.sync`; turn refusal via `beginAgentWait` | `core/schema/agent.go`, `core/agent.go:372` | ~80 |
| `dagger.io/await` attribute and TUI idle rendering | `engine/telemetryattrs`, `dagql/idtui` | ~80 |
| integration tests, SDK regeneration | `core/integration` | ~500 |

The incumbent budgets ~1 week for its Dagger half (`Mailbox`, `schedule`,
`notify(mailbox:)`, refusal — its §6.6), which this design needs too; the
increment is `awaitAny`, `Timer` and `settled`. dagql matches interfaces
structurally, so anything with `id` and `sync` is a `Syncer` automatically
(`dagger:core/schema/coreinterfaces.go:5-8`).

### 6.2 dang (required: nothing further)

The one required change is committed (ae79a04): `SchemaFromSDL` dropped
`interface Syncer implements Node`, so a mixed `[Build, Timer]` literal
joined to `Node` and failed against `[Syncer!]!`. Live introspection was
already right (`pkg/introspection/introspection_dagger.graphql:48`).
Optional, each useful beyond this proposal:

- `loop` checking `ctx.Err()` per iteration (`pkg/dang/stdlib.go:52`) — one
  line; matters only for loops that never call GraphQL.
- Checking list literals against the expected element type. `List.Infer`
  joins bottom-up (`pkg/dang/ast_literals.go:57-84`) and `CommonSupertype`
  returns the first of several incomparable least supertypes in map order
  (`pkg/hm/lattice.go:42-58`); `[container, directory]` shares `Exportable`
  and `Syncer`, so passing it as `[Syncer!]!` can fail nondeterministically.
  ~½ day.
- `case` over an interface-typed handle resolving `__typename`
  (`valueModule` uses the static type, `pkg/dang/ast_patterns.go:490`).
  Not needed by any example here.

### 6.3 Spike

ae79a04 is the loader fix above. c1c137d adds `tests/awaitserver`, a gqlgen
fake of §2.1's core (Syncer awaitables, pure `Timer`, cursor `Mailbox` with
`schedule`, `awaitAny` with modes, timeout, pre-pass and structured
cancellation), and `tests/test_await_engine.dang`, which with **zero
interpreter changes** races two builds (loser cancelled and unwound),
contrasts `FIRST_SETTLED`/`FIRST_SUCCESS`, times out to null, mixes a Timer
into a list, loses a race without losing the message, checks pre-pass order,
takes first-of-N with a cap, and runs the factory loop. `go test ./pkg/...
./tests/` passes.

## 7. Honest weaknesses, and what the incumbent does better

1. **It races only what the engine knows.** Dang speaks to any GraphQL
   endpoint; racing two GitHub queries, or a Dang computation, gets nothing
   here. The incumbent's `select` races any expression against any API with
   no server cooperation. For Dang as a general language, that is decisive.
2. **Index dispatch reads worse than arms with bindings.** `select { m =
   box.next(…) => handle(m) … }` keeps handler beside guard; `if
   (awaitAny([a, b]) == 0)` separates them behind a magic number. Mixed
   lists hit real friction (verified): `asks + [timer]` fails (`cannot use
   [Timer!]! as [Msg!]!`) without `let arms: [Syncer!]! = asks`; indexing
   yields a nullable element; `if (r.index != null)` does not narrow a field
   path (a local `let` does).
3. **Overloading `sync`.** "Force the DAG" and "block until the fact" are
   close, not identical (§8.1).
4. **Round trips.** Each awaitable's ID is resolved before the call
   (`pkg/dang/eval.go:330`). Milliseconds: irrelevant at 5-minute polls,
   visible in tight loops.
5. **More public engine surface** — seven types, two scalars, two enums —
   against the incumbent's grammar rule and four stdlib functions.
6. **Pure Dang still has no time.** Outside Dagger there is no `sleep` or
   `Duration`; the incumbent's prelude scalars and `timeout { }` serve every
   Dang user.

The incumbent got the important things right, and this design adopts them:
guards observe (made structural), a cursor-read mailbox (made the single
sink), absolute deadlines (made the only kind of timer), refusing mailbox
reads in agent turns (enforced). Its own §5.1 concedes the mailbox version
"could even be one arm". **This proposal takes that to its conclusion: once
the engine aggregates and serializes, the language construct is optional.**
If both are wanted, ship this first — it is what every SDK needs — and let a
later `select` compile to `awaitAny` when all its guards are Dagger
awaitables.

## 8. Open questions

1. **`Syncer` or a new `Awaitable` interface?** Reuse makes every existing
   object awaitable for free; a new interface keeps `sync` narrow but needs
   a field per awaitable type and loses builds unless they gain it too.
2. **One field or two?** The timeout makes `awaitAny` nullable even without
   one. Alternatives: an `awaitFirst: Int!` twin, or "timeouts are Timers in
   the list" (which then needs the list annotation in example d).
3. **Tick coalescing.** Skip a tick while an unread one from the same
   schedule exists, or leave it to the program (`ev.at` vs. `now`)?
4. **Durability and detachment** (busybees §3.2): persist mailbox logs and
   schedules with the session; let a `dagger call` from another terminal
   reach a detached session's mailbox.
5. **Evaluator cancellation:** cancel a losing `withExec`, or let it finish
   for the cache? Shared with the incumbent's §8.6.
6. **SETTLED payload size** — inherits agent-messaging §9.
7. **Module-defined `Syncer`s** fall out of structural matching. Are their
   cancellation semantics sane enough to allow by default?
