# Proposal: event streams for concurrency and time

Status: draft, competing with `docs/proposals/concurrency-and-time.md` (the
"incumbent": `select` syntax, `sleep`/`timeout`/`.race`, engine `Mailbox`).
No spike. Code citations are to this repo (vito/dang@597b91c) and to the
dagger repo as `dagger:` (vito/dagger@61744bd, upstream `main` for dagql).

The problem is the one in the incumbent's §0: `dagger call factory run` has to
poll GitHub every 5 minutes, wake **at once** when any of N `Agent`s settles,
wake when a human or agent sends it a message, and schedule follow-ups like
"re-check CI in 10m". Today Dang has no timer and no "first of", and code has
no mailbox to receive from (busybees-as-modules §3.1, agent-messaging §9).

## 1. Core idea and who finds it familiar

**A program that waits iterates a stream.** There is one abstraction, a
lazy, typed `Stream[T]`, and one loop, `stream.each { ev => … }`, which you
leave with `break`. Every wake-up source is a stream: an engine event log,
a clock, or one piece of work. You combine sources with `merge` and narrow
the events with `case`. The incumbent's `select` turns out to be a special
case, `Stream.merge([a, b]).first`.

The design rests on one observation. **The incumbent's best answer for the
factory is already a stream.** Its §5.1 ends by saying that the mailbox-only
variant "reduces the `select` to two arms … could even be one arm". It reads
a cursor on a log, `box.next(after: c)`, in a loop. This proposal makes that
pattern the primary abstraction instead of one guard among several, and it
puts the merging where the ordering is known: in the engine.

People will recognize it from:

- **Elm subscriptions and update**: `Sub.batch [Time.every …, ports]`, with
  state folded over messages. Dang values are copy-on-write, so the factory
  is a fold over its events.
- **Rx, Kotlin Flow, and Rust `Stream`/`StreamExt`**: `merge`, `map`,
  `filter`, `take`, `timeout`, `first`.
- **Kafka and Redis Streams consumers**: an append-only log, a cursor, and
  at-least-once delivery.
- **Go's `for ev := range ch`**: the loop body runs on one goroutine and the
  sources run concurrently.

## 2. Surface

### 2.1 Dagger API (engine side)

This is the incumbent's `Mailbox` (its §4.2), with three changes. Events are
**typed**, the read is a **stream field**, and the cursor is **owned by a
named consumer**.

```graphql
extend type Query {
  "Mint a session-scoped event log. The handle is the capability, as with spawn."
  mailbox(name: String! = ""): Mailbox!
}
type Mailbox {
  id: MailboxID!
  name: String!
  send(message: String!, replyTo: String): MessageEvent!
  """
  The log as a stream. Long-polls until an event exists after `after`, then
  returns it; never consumes. With `consumer`, an omitted `after` resumes
  from that consumer's last acknowledged cursor, and reading after `c`
  acknowledges `c`.
  """
  events(after: String, consumer: String): MailboxEvent! @stream(cursor: "cursor", ack: "ack")
  "Acknowledge `cursor` for `consumer` without reading on (a terminal exiting by break)."
  ack(consumer: String!, cursor: String!): Void
  "Non-blocking page: everything after `after`."
  backlog(after: String, limit: Int = 100): [MailboxEvent!]!
  "Engine-held timer: a ScheduleEvent once after `after`, then every `every`."
  schedule(tag: String!, after: String = "0s", every: String): Schedule!
  "Evaluate `id` in the background; post a WorkEvent when it finishes or fails."
  watch(tag: String!, id: ID!): Void
}
interface MailboxEvent { seq: Int!  cursor: String!  at: String! }
type MessageEvent     implements MailboxEvent { …  text: String!  origin: LLMMessageOrigin!  ref: String! }
type AgentEvent       implements MailboxEvent { …  agent: Agent!  state: AgentState!  reply: String  error: String }
type ScheduleEvent    implements MailboxEvent { …  tag: String!  schedule: Schedule!  firing: Int! }
type WorkEvent        implements MailboxEvent { …  tag: String!  error: String }
type CursorResetEvent implements MailboxEvent { …  reason: String! }
type Schedule { id: ID!  tag: String!  cancel: Void }
extend type Agent {
  notify(subscriber: AgentID, mailbox: MailboxID, on: [AgentState!] = [IDLE, FAILED]): Void
}
```

`@stream(cursor:)` is a **schema hint**, the same kind of thing as
`@expectedType`, which Dang already honours (`pkg/introspection/introspection.go:334`).
It says: "call me repeatedly, passing the previous result's `cursor` back as
`after`." SDKs that ignore the hint still see an ordinary long-poll field.

### 2.2 Dang (stdlib, no new syntax)

`Duration` and `Time` are the incumbent's prelude scalars (its §3.1), adopted
unchanged.

| Kind | Signature | Meaning |
|---|---|---|
| type | `Stream[a]` | A lazy, cold recipe for a sequence of `a`. Nothing runs until a terminal consumes it. |
| source | `ticker(every: Duration!, now: Boolean! = false): Stream[Tick!]!` | Ticks on an absolute schedule. Ticks coalesce (`Tick.missed: Int!`). |
| source | `after(d: Duration!)` / `at(t: Time!): Stream[Tick!]!` | A single tick, then the stream ends. |
| source | `Stream.of { a }: Stream[a]!` | Evaluates the block once, emits its value, then ends. This is the bridge for racing arbitrary work. |
| source | `Stream.from(items: [a]!): Stream[a]!` | Turns a list into a stream (for tests, fakes and replay). |
| source | a field with `@stream`, e.g. `box.events(consumer: "f")` | Typed as `Stream[MailboxEvent!]!`. |
| combine | `Stream.merge(streams: [Stream[a]!]!): Stream[a]!` | N-way merge with a dynamic arity. |
| combine | `s.merge(other: Stream[b]!): Stream[a \| b]!` | Binary merge. The result type widens as `case` arms do. |
| transform | `.map { a => b }`, `.filter { a => Boolean! }`, `.take(n)` | |
| bound | `.within(d: Duration!)`, `.until(other: Stream[b]!)` | Ends the stream at a deadline, or at `other`'s first event. |
| terminal | `.each { a => … }` | **The loop.** `break v` gives the result, as `loop` does. |
| terminal | `.first: a`, nullable | The first event, or `null` if the stream ended empty. |
| terminal | `.fold(initial: b) { acc, a => b }: b`, `.toList: [a]!` | |
| sugar | `sleep(d)` | Means `after(d).each { }`. |

So `select` becomes `Stream.merge([a, b]).first`, a timeout becomes
`.within(d).first`, and "first of N" becomes `xs.map { … }` passed to
`Stream.merge(…).first`. The same vocabulary covers a **quorum**, which
`select` and `.race` cannot express: `….filter { v => v.ok }.take(2).toList`.

## 3. Semantics

**Two kinds of sources.** A *durable* source is an engine log read through
`@stream`. It is cursor-addressed, buffered by the engine, replayable and
edge-triggered. An *ephemeral* source (`ticker`, `after`, `Stream.of`,
`Stream.from`) lives in the interpreter and dies with the terminal that
opened it. The design advice follows from this split: put anything that must
not be lost in a log, and make anything that may be lost (ticks, a race
loser) ephemeral.

**Atomicity: nothing is consumed by being raced.** The incumbent's central
rule is its §2.4, "guards observe; they don't consume". It is a convention
that it cannot enforce. Here the rule holds by construction, because the
only durable read is `events(after: c)`, which is idempotent: the same `c`
always gives the same answer. A durable source's cursor advances **when the
terminal asks for the next item**, which happens after the body has finished
with the current one. Delivery is therefore at-least-once. A body that leaves through `break` or
`return` has *handled* its event, so the terminal acknowledges that event
before it exits. A body that raises has not, so its event stays
unacknowledged and is redelivered. Without that rule, a restarted run would
re-read its own "stop" message. With `consumer:`,
the engine records the acknowledgement, so a restarted run resumes after the
last event it fully handled.

**Edge-triggered, so the "already IDLE wins forever" hazard cannot occur.**
`notify(mailbox:)` reuses the engine machinery that already turns lifecycle
transitions into edges. `installSubscriptionLocked` performs a single
subscribe-time level check (`dagger:core/agent.go:936`). After that,
`queueEventsLocked` fans out only real transitions, tracked per subscriber
in `sub.last`, and suppresses IDLE edges that carry no new turn
(`idleEventDue`, `:950–979`). Code receives exactly one `AgentEvent` per
settle, including a settle that happened while the body was busy. The
incumbent's §5.1 loop can miss that case. It rebuilds
`busy = workers.reject { settled }` on every iteration, so a worker that
settles *during* a tick is already IDLE when the next `select` starts. It
gets no arm, and `settle(w)` waits for the next 5-minute poll. That breaks
the "wake immediately" requirement exactly when the factory is busiest.

**Backpressure: the log is the buffer.** Events that arrive while the body
runs are appended to the engine log, and the next pull reads them in order.
Inside the interpreter, everything is pull-based. A merge runs one goroutine
per source, and each source has **one slot**: it is not pulled again until
its item has been delivered. No Dang-side buffer is unbounded. A tick that
arrives while the slot is full coalesces into `missed`, as Go's `time.Ticker`
drops ticks. A slow body therefore delays events but never loses durable
ones, and it never builds up a queue of stale ticks.

**Fairness: merge order is event order.** Within one log, order is `seq`
order. That order is total, the same for every reader, and replayable. This
is the reason for the advice to merge at the source: the factory has one
log, so it has one deterministic order. A Dang-side `merge` delivers in the
order items arrive at the merge, and it serves ready slots **round-robin**
starting after the source it served last. A busy source therefore cannot
starve a pending tick, because the tick waits at most one item per other
source. The incumbent leaves fairness "not specified" (§2.5) and needs
absolute deadlines to avoid starvation. Here the same guarantee is a
property of `merge`.

**Cancellation and lifetime are structured.** A `Stream` value is a cold
recipe, like a lazy GraphQL handle, and building one runs nothing. A terminal
(`each`, `first`, `fold`, `toList`) opens the sources under a child context.
When the terminal ends, whether by completion, `break`, `return` or `raise`,
it cancels **and waits for** every source goroutine. That is the same
contract as `evalParallel` (`pkg/dang/eval.go:1661`). Cancelling a source
aborts its in-flight GraphQL request through `query.Execute(ctx)`
(`eval.go:257`). For `events`, this is harmless: the long-poll changed
nothing, and the next reader re-reads from the same cursor. **Engine
subscriptions are not owned by streams.** `notify(mailbox:)`, `schedule` and
`watch` belong to the mailbox and live as long as the session. Closing a
reader therefore loses nothing, and events keep accumulating for the next
reader.

**Control flow is decided by when a block runs.** Terminal blocks run
synchronously on the caller's goroutine, inside the terminal's call
frame. `break`, `continue` and `return` work exactly as they do in a
list's `.each`. Concretely, a `BreakException` returned from the block is
caught by the terminal's `FunCall` frame (`pkg/dang/ast_expressions.go:334–361`),
and writes to enclosing bindings (`f = f.tick(…)`) go through. Transformer
and source blocks (`map`, `filter`, `Stream.of`) are **escaping**: they run
later, possibly on a source goroutine. They are therefore inferred behind a
function control boundary (`contextWithInferFunctionControlBoundary`,
`pkg/dang/control_flow.go:135`), which makes `break` inside them a compile
error, and they evaluate in sealed scopes, as `{{ }}` fields do. The result
is **concurrency at the edges and a sequential centre**: the body never runs
concurrently with itself, so it cannot race on the state it folds.

**Failure.** If a source raises, the terminal raises. The other sources are
cancelled and awaited, which is `{{ }}`'s fail-fast rule. To tolerate a
failing source, rescue inside it: `Stream.of { a.sync rescue null }`. If the
body raises, the terminal raises and the cursor is **not** acknowledged, so
the event is redelivered after a restart (poison events are discussed in
§8). The engine reports a `watch`ed recipe that fails as data
(`WorkEvent.error`), not as a stream failure.

**Typing.** `Stream[a]` is a built-in generic, the third after `[a]` and
`Map[a]` (`pkg/dang/types.go:66–106`, `:187`). An engine event is an
interface value. Dang loads it as its concrete type, so `case (ev) {
s: AgentEvent => … }` narrows exactly as the GraphQL union tests do
(`tests/test_union_graphql.dang`). A terminal's result type comes from its
`break` values, through `mergeCallBreakTypes`, as with `loop`. `.first` is
nullable, because a stream that ends empty, or ends at `.within`, has no
first event. Heterogeneous `s.merge(t)` widens with
`mergeControlResultTypesTagged` (`pkg/dang/union_provenance.go:40`), so
provenance notes say which source contributed which member.

**Restore.** The cursor *is* the state. A cursor is opaque
(`"<incarnation>:<seq>"`), modelled on agentcontrol's `Namespace{Session,
Trace, Incarnation}`, where "restoring a handle in a new session creates a
new incarnation" (`dagger:engine/agentcontrol/control.go:16–23`). Reading a
cursor that belongs to another incarnation yields one `CursorResetEvent`.
That converts edges missed across the restore into an explicit
level-resync: the factory responds by doing a full tick.

**Agent turns.** agent-messaging §3 says a turn never blocks. `events`
called while an agent is in the context (`AgentFromContext`) is **refused**
with a message that teaches the fix: "subscribe the agent with
notify(subscriber:)". This is the incumbent's §4.3 rule, and it applies
unchanged. Ephemeral terminals (`ticker(…).each`, `.within(…).first`) get
the incumbent's advisory blocking budget. Agents and code end up sharing a
vocabulary: an agent's mailbox receives `notify` events as messages
(`eventTextLocked`, `dagger:core/agent.go:999`), and code receives the same
transitions as typed `AgentEvent`s.

**Trace.** On append, every event records the span context of its
producer: the state transition, the `send` call, or the timer. The span
for each `events` pull carries an OTel **link** to that producer. With the
incumbent's host hook (its §6.5), each `.each` iteration becomes a span
named after its event, such as `AgentEvent dev/42 IDLE`, and the body's
GraphQL calls nest under it. A factory run then reads as a list of handled
events, each linked to its cause. A `select` loop instead issues, on every
wake, one `wait` per busy worker plus `box.next` plus the sleeps. It then
cancels all but one of them: |busy|+2 requests per wake, and a trace full
of cancelled guards.

**What about agentcontrol?** It is the right *producer hook and archive
format*, not the live source. Its records are complete projections ("not a
delta", `control.go:30`) that are ingested asynchronously from telemetry
(`dagger:dagql/dagui/agent_control.go:36`). A consumer that lags sees
IDLE→RUNNING→IDLE coalesced into "no change", which is acceptable for a UI
and wrong for "every settle". It also carries no capability boundary. The
proposal therefore reuses three of its parts:

- the emission points (`rt.control.subscription`, `core/agent.go:934`, sits
  next to `queueEventsLocked`);
- the revision and incarnation discipline;
- the archive, since mailbox records ride telemetry the same way, so the TUI
  can show a mailbox lane with its consumer cursor, and restore can rebuild
  the log.

## 4. Layering

| Layer | What | Why there |
|---|---|---|
| Language syntax | **nothing** | No keyword, no grammar change, no tree-sitter or editor work. This is the opposite of the incumbent's §6.1. |
| Type system | the `Stream[a]` generic; an *escaping block* flag on builtins | These are the two pieces a library cannot add from outside. |
| Dang stdlib | the `Stream` runtime, sources, combinators and terminals; `Duration`/`Time`; interpreting `@stream` | They are pure or interpreter-local and work against any GraphQL API. "Dang knows no Dagger" holds, because `@stream` is a schema convention. |
| Dagger API | the `Mailbox` log, typed events, `notify(mailbox:)`, `schedule`, `watch`, consumer acknowledgements, refusal inside turns | These are ordering, durability and dynamic membership, which only the engine can own. |

**Non-Dang SDKs get the important half.** The engine log is
SDK-neutral, so a Go factory is
`for { ev, _ := box.Events(ctx, opts); handle(ev); opts.After = ev.Cursor }`,
with the same edge-triggering, ordering, at-least-once delivery and restore
behaviour. Neither `select` nor `Stream` is required. `@stream` also gives
other clients something to act on later: CLI `--follow`, MCP, and the TUI
tailing a mailbox. Dang's contribution is the ergonomics.

## 5. Worked examples

### (a) The factory run loop

```dang
type Factory {
  # … state as in busybees-as-modules §2.1, plus `box: Mailbox` …

  run(source: Workspace!): Void @cache(policy: Never) {
    let box = mailbox(name: "factory")             # humans/foreman send here too
    let f = withMailbox(box)                       # tick's spawns call w.notify(mailbox: box)
    f.workers.values.each { w => w.notify(mailbox: box) }  # adopt live workers (one level event each)
    box.schedule(tag: "poll", every: "5m")         # the clock is just another producer

    # ONE totally ordered, edge-triggered, at-least-once stream. No merge needed:
    # the engine already merged settles, messages and timers into one log.
    box.events(consumer: "factory").each { ev =>
      case (ev) {
        s: ScheduleEvent => {
          f = if (s.tag == "poll") { f.tick(source, full: true) } else { f.recheck(s.tag) }
        }
        a: AgentEvent => {
          f = f.settle(a.agent).tick(source, full: false)    # cheap local pass
        }
        m: MessageEvent => case (m.text) {
          "stop" => break                          # leaves .each; "stop" is acked on the way out
          "poll" => { f = f.tick(source, full: true) }
          else => f.foreman.send(m.text)
        }
        r: CursorResetEvent => {
          f = f.tick(source, full: true)           # restored: resync by level
        }
      }
      # follow-ups ("re-check CI in 10m") become engine timers, so future events
      f.takeFollowUps.each { u =>
        box.schedule(tag: "ci:" + toString(u.issue), after: u.after)
      }
      f = f.clearFollowUps
    }
    null
  }
}
```

The loop has no cursor variable, no `busy` set, no `nextPoll` deadline, and
no `followUps` list to race. The incumbent's version (its §5.1) keeps all
four by hand. The ordering of events, their durability, and the set of
things that can wake the loop (which grows as `tick` spawns workers) all
live in the engine, where they survive restore. If the poll should stay a
Dang-side clock, it merges in:
`box.events(consumer: "factory").merge(ticker("5m", now: true))`, and a
`t: Tick =>` arm handles it.

### (b) Race two registries

```dang
let registry = Stream.merge([
  Stream.of { container.from("docker.io/library/alpine:3.20").sync
              "docker.io" },
  Stream.of { container.from("mirror.gcr.io/library/alpine:3.20").sync
              "mirror" },
]).first!                                    # the loser is cancelled and awaited
```

### (c) Time-box a build

```dang
let built = Stream.of { source.dockerBuild.sync }.within("10m").first
if (built == null) {
  raise "build took longer than 10m"
}
```

### (d) First of N reviewers, at most 1 hour

```dang
let verdict = Stream.merge(reviewers.map { r => Stream.of { r.send("review PR 57").response } })
  .within("1h")
  .first ?? "no review in time"

# the same pipeline does a quorum, which select/race cannot express:
let approvals = Stream.merge(reviewers.map { r => Stream.of { r.send("review PR 57").response } })
  .filter { v => v.startsWith("APPROVE") }
  .take(2)
  .within("1h")
  .toList                                    # 0, 1 or 2 approvals by the deadline
```

## 6. Implementation sketch and sizing

**Dang (this repo).**

1. `StreamType{hm.Type}` next to `MapType` (`pkg/dang/types.go:187`), plus a
   `"Stream"` case in `AppliedTypeNode.Infer` (`:96`). Add
   `StreamTypeModule` next to `ListTypeModule`/`MapTypeModule`
   (`pkg/dang/ast_literals.go:21`). Then **generalize** the copy-pasted
   receiver dispatch for List and Map (`pkg/dang/ast_expressions.go:792–829`)
   into one table keyed by container type, instead of adding a third copy.
2. Builder support: `StaticMethodBuilder.Block` (it has none today,
   `pkg/dang/builtins.go:414–469`), and `.EscapingBlock()` on the method and
   static builders. Escaping blocks are inferred under
   `contextWithInferFunctionControlBoundary` and evaluated in
   `scope.Derive(true)`.
3. `pkg/dang/stdlib_stream.go` holds `StreamValue{Elem; open func(ctx)
   (iter, error)}`, with `iter.Next(ctx) (Value, error)` and `Close()`.
   - Sources are timer-backed (`ticker` uses absolute deadlines and
     coalesces), `Stream.of`, and `Stream.from`.
   - `merge` uses one-slot round-robin.
   - `within` is `context.WithDeadline`, and `until` is an internal merge
     with a stop signal.
   - Terminals reuse `callFunc` (`pkg/dang/stdlib.go:1182`) and return
     `BreakException` unchanged, so the `FunCall` frame catches it, as it
     does for `loop` (`stdlib.go:41–57`).
4. `@stream(cursor:)`: when importing the schema
   (`populateSchemaFunctions`, `pkg/dang/eval.go:463`), give the field the
   type `Stream[T]!`. Its iterator selects `{cursor __typename id}`, which
   forces the long-poll. It then loads the concrete type with
   `loadObjectFromID` (`eval.go:843`), so that `valueModule`
   (`pkg/dang/ast_patterns.go:490`) dispatches `case` on the real type.
5. Make `loop` and each terminal check `ctx.Err()` (the incumbent's spike
   does this too).
6. Tests: `tests/test_stream_*.dang` (merge, first, within, take, break and
   return from `.each`, a source raising, a compile error for `break` in
   `.map`). Add a `tests/gqlserver` field `events(after:)` with `@stream`.
   Go tests under `testing/synctest` cover round-robin fairness,
   tick coalescing, and "every source awaited on break"
   (`pkg/dang/parallel_selection_test.go` is the model).

**Dagger.**

1. `core/mailbox.go`: an append-only log with `seq`, an incarnation, a
   broadcast channel for waiters, consumer acknowledgements, and producer
   span contexts. It lives in the session runtime table next to
   `AgentRuntimes`.
2. `core/schema/mailbox.go`: the event interface and its implementers via
   `dagql.Interface` (`dagql/interfaces.go:20`). Real GraphQL subscriptions
   are out of scope: dagql rejects them (`dagql/server.go:1129`,
   "subscriptions not supported"), so a long-poll field is the transport.
   Since `@stream` hides the transport, switching to subscriptions later does
   not change the Dang surface.
3. `core/agent.go`: a mailbox subscriber kind, so that `queueEventLocked`
   (`:984`) appends an `AgentEvent` instead of enqueueing message text.
   Edge and level logic is reused as is.
4. Timers for `schedule`; background evaluation for `watch`. Mailbox
   telemetry records sit beside agentcontrol. `events` is refused inside an
   agent turn.

| Piece | Effort |
|---|---|
| `Duration`/`Time` (shared with the incumbent) | 1–2 d |
| `Stream[a]` type, dispatch refactor, escaping blocks, `StaticMethod.Block` | 2 d |
| Stream runtime: sources, merge, combinators, terminals | 2 d |
| `@stream` import, gqlserver fixture, synctest suite, docs | 2 d |
| Dagger: `Mailbox` log, typed events, consumers, `notify(mailbox:)`, `schedule` | ~1 week (≈ the incumbent's) |
| Dagger: `watch`, telemetry and span links, turn refusal, Dang SDK bump | 3–4 d |

Dang total: about 7–8 days, against roughly 4–6 days for the incumbent. The
extra goes into the type system instead of the grammar and editors.

## 7. Honest weaknesses and what the incumbent does better

- **One-off races are wordier.** Compare
  `Stream.merge([Stream.of { a }, Stream.of { b }]).first!` with
  `select { a => …; b => … }`. Each arm also loses its own body and binding:
  with heterogeneous Dang-side sources you have to `map` each one to a tagged
  value and then `case` on it. `select` reads better for exactly the
  registry-mirror case.
- **`timeout(d) { … }` reads more naturally** than
  `Stream.of { … }.within(d).first`. This proposal would adopt `timeout` as
  sugar if asked.
- **Coldness is a new trap.** Iterating a stream twice re-runs its sources,
  including the work inside `Stream.of`. This is the lazy-handle lesson
  again, and the laziness lint (`tailLeavesLazyHandle`) has to learn about
  streams.
- **More type-system work**: a third generic container, escaping blocks, and
  widening for binary merges. The incumbent puts its complexity in a grammar
  rule that a reader sees once.
- **`watch` is unstructured concurrency** in the engine. Mailbox lifetime
  bounds it, but it does outlive the expression that started it.
- **At-least-once delivery requires idempotent bodies.** The factory's
  reconcile-style `tick` is idempotent; arbitrary code may not be.
- **Engine events cannot be faked in Dang.** `Stream.from` lets you test a
  loop over Dang values, but not one that dispatches on engine
  `AgentEvent`s. Testing those needs the gqlserver fixture or the engine.

The incumbent does three things better: ad-hoc races between unrelated
expressions, a smaller interpreter change, and immediate familiarity for
tokio and Go users. Its claim that "Dang programs mostly race GraphQL calls"
does not hold for the problem as stated, though. Every wake source in
busybees §3.1 is an *event* (four clocks, agent settles, messages), and
agent-messaging §4.3 already concluded that lifecycle should arrive as
messages, not be awaited. For races between GraphQL calls, streams still
work, at the cost of one extra word per arm.

## 8. Open questions

1. **Heterogeneous merge typing.** Should `s.merge(t)` widen to a union, or
   require `map`ping both sides to a common type first? The second is simpler
   and less magical.
2. **`@stream` or an explicit adapter** for third-party cursor APIs (such as
   GitHub's `endCursor`)? A `Stream.unfold(seed) { s => {{value, next}} }`
   would cover APIs that do not carry the hint.
3. **Retention.** When can the acknowledged prefix of a log be trimmed? And
   how do mailboxes and schedules become durable across sessions
   (busybees §3.2)?
4. **Poison events.** After a body has raised K times on the same cursor,
   should the event be skipped, dead-lettered, or left to stop the factory?
5. **Fan-out or work-sharing** when two readers use the same `consumer`:
   independent cursors, or a Kafka-style consumer group?
6. **Hybrid.** If `select` ships anyway, should its guards accept streams
   (`ev = s.next =>`)? Then the two designs would share one engine log, and
   `select` would only be sugar over `merge.first`.
7. **`watch`'s scope.** Is "run this ID in the background and tell my log
   when it is done" the right escape hatch, or should background work always
   be an `Agent` or a `Service`?
