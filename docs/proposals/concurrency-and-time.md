# Proposal: concurrency and time in Dang

Status: draft, with a spike on this branch (see [Spike](#spike)).

This proposal gives Dang a way to wait for **whichever of several things
happens first**, and a way to wait for **time**. Today a Dang program can
only do work concurrently and wait for all of it (`{{ }}`). It cannot race
work, sleep, give up after a deadline, or be woken by a clock.

## 0. The problem

The motivating program is an orchestrator written as a Dagger module in
Dang, run as `dagger call factory run`. Its job is to stay up and react:

- poll GitHub (a container exec) every 5 minutes;
- wake **immediately** when any of N spawned `Agent`s settles (IDLE or
  FAILED);
- wake when a human or agent sends it a message;
- on each wake, diff state, `send` updates to a foreman agent or spawn
  workers, and schedule follow-ups ("re-check CI in 10m").

The Dagger-side design notes say why this is hard today. In
`hack/designs/notes/busybees-as-modules.md` (dagger repo), §2.1 picks "a
long-running driver session" that loops `tick` and sleeps as the only option
that works now. §3.1 ("Time: nothing wakes on a clock") notes there is "no
sleep but a nonce-busted `sleep` exec, and no `select` over 'timer or agent
settled'". `hack/designs/agent-messaging.md` §9 leaves "combinators for code:
`awaitAny`/`awaitAll`" open, to be revisited when someone needs them. The
factory needs them.

Here is what a Dang module can call today
(`core/schema/agent.go`, dagger repo):

| Call | Blocks? | Notes |
|---|---|---|
| `Agent.send(message, replyTo)` | no | enqueues; returns `AgentMessage` ID (`agent.go:74`) |
| `AgentMessage.response` | yes | until an explicit reply or the consuming turn ends (`agent.go:174`) |
| `Agent.wait` | yes | until IDLE/FAILED/STOPPED (`agent.go:123`); the engine side respects context cancellation (`core/agent.go:2457`, `case <-ctx.Done()`) |
| `Agent.state`, `snapshot`, `error` | no | read-only projections |
| `Agent.notify(subscriber: AgentID!, on)` | no | lifecycle events go to another **agent's** mailbox (`agent.go:129`) |
| `pause`, `resume`, `stop`, `reseed` | no | |

Several things are missing: there is no sleep, no timer, no mailbox code can
receive from, and no way to wait on several things at once. Dang has none of
these either. Nothing under `pkg/dang` calls `time.Sleep`, and there is no
`Duration` type.

## 1. Principles and where things live

Dang's README ranks **familiarity** over theory and **ergonomics** over
syntactic purity ("embrace keywords and first-class syntax for common
patterns"), and asks Dang to "be a leaf in the wind". It should not take much
thought. Applied to concurrency:

1. **One construct for "first of".** People who know Go, Rust (tokio) or
   Erlang should recognize it right away, and it should work on any
   expression, not only special "awaitable" values. Dang already has an
   effect system of sorts: GraphQL calls block, and `{{ }}` runs things
   concurrently. The new construct should use that, not add futures,
   channels or promises as values.
2. **Structured concurrency only.** No work outlives the expression that
   started it. Dang already works this way: `evalParallel`
   (`pkg/dang/eval.go:1661`) waits for every task it starts, cancels the rest
   when one fails, and picks one error to report. New constructs keep that
   contract. There is no `spawn`/`go` for code. Long-lived concurrent actors
   are Dagger `Agent`s and services, which have identity, a trace, and
   lifecycle verbs.
3. **Time is data.** Durations and instants are scalars with literal
   coercion, like `Path` (`pkg/dang/prelude/path.dang`). They are not
   ad-hoc strings.
4. **Dang knows no Dagger.** "Architecturally, Dang is decoupled from Dagger;
   it just speaks GraphQL" (README). Anything that needs engine state, such
   as mailboxes or timers that outlive a call, goes in the Dagger API, and
   Dang simply calls it.

Each primitive therefore lives in one of three layers:

| Primitive | Layer | Why |
|---|---|---|
| `select { … }` | **(a) language syntax** | It races *expressions*. Its arms need lazy evaluation, bindings and per-arm bodies that run in the caller's scope. A builtin cannot express this (§2.6). |
| `xs.race { x => … }` | **(b) stdlib** | The dynamic N-way form. It is an ordinary block-taking list method, like `.map`. |
| `sleep`, `timeout`, `every`, `Duration`, `Time` | **(b) stdlib / prelude** | They work against any API and can be cancelled exactly through the evaluation context. In Dagger they already run inside the engine, because the Dang SDK interprets modules in-process (`core/sdk/dang/v2`, dagger repo), so an interpreter timer is an engine timer for the lifetime of the call. |
| `Mailbox` (receive in code), `Agent.schedule`, `Mailbox.schedule`, `notify` to a mailbox | **(c) Dagger API** | These hold state across calls and outlive any one call (timers, queued messages). Only the engine can own them. |

Why not an engine-side `Query.sleep`? It would show up in the trace and could
be marked never-cached. But it costs a round trip, would only exist for Dang
programs that talk to Dagger, and duplicates what one `select` on the context
does. Visibility in the trace is better handled with a host hook (§6.5).

## 2. `select`: waiting for the first of several things

### 2.1 Syntax

```dang
select {
  m = inbox.next(after: cursor) => handle(m)             # bind the guard's value
  w = busy.race { w => w.wait } if (!busy.isEmpty) => settle(w)
  sleep(until: nextPoll) => poll                         # a timer is just a guard
  else => idle                                           # all arms disabled
}
```

Each **arm** is written `[binding =] guard [if (cond)] => body`:

- **guard**: any expression. All enabled guards are evaluated concurrently,
  and the first one to finish wins.
- **binding** (optional): names the winning guard's value inside `body`.
- **`if (cond)`** (optional): evaluated in order *before* the race. A false
  condition turns the arm off for this evaluation (tokio's `, if cond`).
- **body**: runs only for the winning arm, after the race is over.
- **`else => body`** (optional, must come last): runs when *every* arm is
  turned off. If all arms are off and there is no `else`, `select` raises
  instead of hanging forever.

There is no special `after` keyword for timeouts, as Erlang has. A timeout is
just a guard that sleeps. That removes one concept, and the guard can use
anything for its deadline: `sleep("30s")`, `sleep(until: t)`, or a GraphQL
call.

This is the shape of Rust's `tokio::select! { v = fut => body, … }`. It is
familiar, it reads like a `let` binding, and `=` binds in Dang already. The
`=>` arms and the `else` arm mirror `case` and `rescue` clauses
(`pkg/dang/dang.peg:1246`, `:734`). Section 2.6 compares other spellings.

### 2.2 Semantics

1. Evaluate each arm's `if` condition in source order, in the enclosing
   scope. Collect the arms that are on. If none are, run `else` or raise.
2. Start every enabled guard on its own goroutine, under a context that can
   be cancelled. Each guard gets a *sealed* child scope (`scope.Derive(true)`),
   as each `{{ }}` field does, so writes a guard happens to make stay private.
3. The first guard to **finish** wins, whether it returns a value or raises.
   An error wins too: if the first guard to finish raised, the whole `select`
   raises that error, matching `{{ }}`'s fail-fast rule. To ignore a failing
   guard, rescue inside it: `m = inbox.next(after: c) rescue null => …`.
4. Cancel every losing guard, and **wait for them to unwind** before going
   on. When `select` returns, no guard is still running.
5. Run the winning body on the caller's goroutine, in an *unsealed* child
   scope that holds the binding. Reassigning an enclosing binding writes
   through to it. `break`, `continue` and `return` in a body behave exactly as
   in a `case` clause, so `loop { select { … } }` is the idiom.

**Control flow cannot leave a guard.** A guard is raced and may be
cancelled, so a `break` or `return` inside it has nowhere sensible to go. The
type checker infers guards behind a function control boundary
(`contextWithInferFunctionControlBoundary`), so `break` in a guard is a
compile error. In the spike the message is the generic
`break outside of block-taking call`. A dedicated message is follow-up work.

### 2.3 Typing

- A binding's type is its guard's inferred type, so `m = inbox.next(…)` is
  `MailboxMessage!`. Writing a type pattern in a binding (`m: Foo = …`) is not
  supported. Use a `case` in the body.
- Conditions must be `Boolean!` (`condition must be Boolean, got String!`),
  the same rule `if` enforces.
- The result type merges the bodies, as `case` clauses do
  (`mergeControlResultTypesTagged` with `armOrigin("select arm", …)`). Bodies
  of different types widen to a union with provenance notes. The result is
  **never nullable just because it is a `select`**: exactly one body runs,
  or it raises. (Compare a `case` with no `else`, which is nullable.)
- `else` must come last (`unreachable arm: follows the else arm on line N`),
  following `checkClauseReachable`'s rule for `case`.
- **Laziness lint.** A guard that ends in a lazy GraphQL handle, such as
  `container.from("x").withExec([...])`, finishes immediately, because
  nothing has run yet. Dang already detects this for `rescue`
  (`tailLeavesLazyHandle`, `pkg/dang/rescue_analysis.go:459`). `select` would
  reuse it to warn: `a lazy Container makes this guard finish immediately;
  end it at an execution point like .sync or .stdout`.

### 2.4 Guards observe; they don't consume

This rule matters more than anything else in the proposal. **A guard should
only observe state, so that losing the race loses nothing.**

Go's `select` is atomic: a channel receive either happens or it doesn't. A
race over remote calls is not. Suppose a losing guard was
`mailbox.receive`. The server may have already dequeued the message when the
cancellation arrives, and then the message is lost. Neither GraphQL nor the
HTTP client offers a two-phase commit that could prevent it.

So every blocking call meant to be used as a guard should be
**level-triggered and idempotent**:

- `Agent.wait`, `AgentMessage.response` and `sleep` already are. They
  observe a state and change nothing.
- The mailbox proposed in §4 is a **log read with a cursor**:
  `next(after: seq)` returns the first message with a sequence number above
  `seq` and does not remove it. The program moves its own cursor forward in
  the body. If that guard loses, the next iteration simply reads the same
  message again.

Level-triggered guards bring one hazard of their own: a condition that stays
true wins every time. `workers.race { w => w.wait }` returns at once if any
worker is *already* IDLE, so a loop would spin on it. The fix is to race only
the workers you expect to change (`busy = workers.filter { w => w.state ==
RUNNING }`), or to use lifecycle events in a mailbox (§4), which are a log
and so naturally edge-triggered.

The proposal does **not** try to enforce this rule. Dang cannot know which
GraphQL fields have side effects. It is documented, and every Dagger API
added here is designed to follow it.

### 2.5 Cancellation, fairness, nesting, and the trace

**Cancellation reaches GraphQL.** A guard's GraphQL request runs with the
guard's context. `GraphQLFunction.Call` runs scalar leaves through
`query.Execute(ctx)` (`pkg/dang/eval.go:257`), as does selection
(`ast_expressions.go:1929`, `:2277`). In Dagger, the Dang SDK sends those
requests over HTTP to a nested-client server (`core/sdk/dang/shared/shared.go:112`).
Cancelling a guard therefore aborts its HTTP request, which cancels the
resolver's context on the engine side. For the waits this proposal relies on,
that is safe: `WaitSettled` returns `context.Cause(ctx)` and changes nothing
(`core/agent.go:2476`). Work that already ran stays done. A `withExec`
abandoned halfway may or may not be cancelled in BuildKit, and work shared
with other callers keeps running for them. A cancelled call is never cached
as a success. All of this is why §2.4 says guards should observe.

**Loops in the language can be cancelled too.** A guard (or a `{{ }}`
sibling) that spins in a `loop` without touching the network could not be
stopped before, because `loop` never checked its context. The spike makes
`loop` check `ctx.Err()` between iterations. This also fixes a gap in the
existing `{{ }}` fail-fast behavior.

**Fairness.** When several guards finish at almost the same moment, which
one wins is **not specified**. The implementation prefers the lowest index
among guards it can already see as finished, but that is best-effort: the
goroutine scheduler decides what counts as "already". The spike test
`["a","b","c"].race { x => x }` showed `"c"` winning. Go picks at random;
tokio is random unless you write `biased;`. Programs must not depend on the
order, and in practice they do not need to:

- Starvation by timer reset (a relative `sleep("5m")` restarts on every
  iteration, so a busy mailbox postpones polling forever) is solved with
  **absolute deadlines**: `sleep(until: nextPoll)` (§3). A deadline that is
  already due returns at once.
- To give an arm priority, check its state before the `select`, not by
  relying on arm order.

**Nesting.** A guard is an expression, so it may itself be a `select`, a
`timeout`, a `{{ }}` or a `.race`. Cancellation passes down through the
context, and the "nothing outlives it" guarantee holds at every level.

**No `default` arm in the Go sense.** Go's `default` means "nothing is ready
right now". For remote guards, "right now" has no useful meaning, because
every guard costs a round trip. A non-blocking check is a plain read
(`agent.state`, `mailbox.messages(after: c)`). `else` has tokio's meaning:
it runs when all arms are switched off.

**Trace.** Dang has no tracing of its own (no `otel` imports in `pkg/`). In
Dagger each guard's GraphQL call is its own span, and cancelled losers show
up as cancelled spans, so the race is visible with no extra work. Showing
`select`, `sleep` and `timeout` as spans themselves needs a host hook
(§6.5).

### 2.6 Why `select` is syntax, not a builtin

Dang prefers builtins: `loop`, `each` and `assert` are all block-taking
functions in `pkg/dang/stdlib.go`. A builtin cannot express `select`:

- **Arguments are evaluated before the call.** `FunCall.Eval` runs
  `evaluateArguments` before `callFunction` (`ast_expressions.go:329`), so in
  `race(a.sync, b.sync)` both would finish one after the other before `race`
  ever ran.
- **A function takes at most one block** ("At most one block parameter per
  function/constructor"), and there are no lambda literals outside block
  arguments, so `race({ a }, { b })` cannot be written.
- **A record does not help.** `race { {{ a: …, b: … }} }` builds a record,
  and `ObjectLiteral` forces every field through `evalParallel`, which waits
  for all of them.
- **Arm bodies have to run in the caller's control flow.** A body that says
  `break` must break the *enclosing* `loop`. A block-taking builtin would
  become the target of that `break` itself (`BlockCallFrame`,
  `ast_expressions.go:334–360`).

The dynamic case, N copies of the same guard, *does* fit a builtin, so it is
one: `[T]!.race { x => guard }`, returning the first result. The two work
together: `w = busy.race { w => w.wait } => settle(w)`.

## 3. Time

### 3.1 Types (prelude)

```dang
scalar Duration { new(raw: String!) { … } }   # "300ms", "1.5s", "5m", "1h30m" (Go syntax)
scalar Time     { new(raw: String!) { … } }   # RFC 3339, normalized to UTC
```

Both follow the `Path` pattern (`pkg/dang/prelude/path.dang`). A `new()`
hook backed by a Go builtin validates and normalizes the value. A string
**literal** placed where a `Duration!` is expected coerces to it, so
`sleep("5m")` type-checks, and a mistake like `sleep("5 minutes")` fails at
runtime with a clear message. A string *variable* needs `Duration(s)`, the
same literal-only rule as `Path`. Both scalars turn back into `String!` at
GraphQL boundaries, so passing them to Dagger arguments typed `String` just
works.

Methods: `Duration.seconds: Float!`, `Duration.add(other)`;
`Time.now` (static), `Time.add(d: Duration!): Time!`,
`Time.until(other): Duration!`, `Time.isBefore(other): Boolean!`. `Time.now`
is impure, as `Random` is.

### 3.2 Functions (stdlib)

| Function | Meaning |
|---|---|
| `sleep(duration: Duration!)` / `sleep(until: Time!)` | Wait, then return `null`. Returns early, raising the cancellation error, if the surrounding evaluation is cancelled. `until` in the past returns immediately. |
| `timeout(duration: Duration!) { a } -> a?` | Run the block under a deadline. Returns its value, or **null** if the deadline comes first (the block's work is cancelled). Errors and `break`/`return` pass through unchanged. Sugar for `select { v = { … } => v; sleep(d) => null }`, except that it is nullable on purpose. |
| `every(interval: Duration!) { … }` | A loop that runs at a fixed rate. Runs the block, then sleeps until the next tick, skipping ticks it has missed, so it does not drift or fire in bursts. `break` exits. For the simple "poll every 5m" case where nothing else can wake the loop. |
| `[T]!.race { x => b } -> b` | §2.6. An empty list raises (`nothing can ever finish`) instead of hanging. |

`timeout` returns null rather than raising because a timeout is usually an
expected outcome ("errors are for errors, not control flow", per the
control-flow reference). To make it fatal, test the result for `null` and
raise (§5.2).

### 3.3 Timers that outlive a call (Dagger API)

A Dang timer lives only as long as the function call running it. To wake an
**agent**, for example a foreman in `dagger agent` that should look at
GitHub every hour with no host program looping, the engine has to hold the
timer. This is busybees §3.1's "primitive (c)", generalized:

```graphql
type Agent {
  """
  Enqueue an event-origin message on this agent's clock: once after
  `after`, then every `every` if set. Events ride the ordinary mailbox
  (agent-messaging §4.3) and never relaunch a STOPPED agent.
  """
  schedule(message: String!, after: String!, every: String): ID!  # @expectedType Schedule
}
type Schedule { id: ID!  cancel: Void }
```

As agent-messaging §3 requires, timers deliver **messages, not callbacks**.
A woken agent starts an ordinary turn. `Mailbox.schedule` (§4) is the same
mechanism for code. The schedules live in the session's runtime table, next
to agent runtimes, and the trace shows `schedule` and each firing as spans.

## 4. Receiving messages in code

### 4.1 What's missing

Module code is not an agent, so it has no mailbox. The factory needs one
place where three things arrive: human or foreman commands, lifecycle events
from N workers, and its own scheduled reminders. Its `Factory` value is
immutable (copy-on-write), so a tool call such as `factory.pause` made in
*another* call cannot reach the running `run` loop through module state. The
channel has to be held by the engine.

### 4.2 Proposal: a `Mailbox` capability

```graphql
extend type Query {
  "Mint a fresh, session-scoped mailbox. Like spawn, the handle IS the capability."
  mailbox(name: String! = ""): Mailbox!
}
type Mailbox {
  id: MailboxID!
  name: String!
  "Append a message. Origin (agent/user/event) is resolved at enqueue, as for Agent.send."
  send(message: String!, replyTo: String): ID!          # @expectedType MailboxMessage
  "Block until a message with seq > after exists; return the FIRST such. Never consumes."
  next(after: Int! = 0): MailboxMessage!
  "Non-blocking read of everything after `after`."
  messages(after: Int! = 0): [MailboxMessage!]!
  schedule(message: String!, after: String!, every: String): ID!
}
type MailboxMessage {
  seq: Int!
  text: String!
  origin: AgentMessageOrigin!   # reuse: kind USER | AGENT | EVENT, agentName, messageId, replyTo
}
extend type Agent {
  # widen notify: deliver lifecycle events to a mailbox instead of an agent
  notify(subscriber: AgentID, mailbox: MailboxID, on: [AgentState!] = [IDLE, FAILED]): ID!
}
```

- **A cursor, not destructive receive** (§2.4). `next(after: n)` always
  gives the same answer for the same `n` once that answer exists. Losing a
  race costs nothing, and replaying an ID cannot swallow a message. The
  program keeps its own cursor.
- **One place to wake up.** With `notify(mailbox:)` and `schedule`, the
  factory's workers, humans and clocks all write to *one* log. The whole
  run loop can then be `loop { m = box.next(after: c) … }`. `select` is
  still the general tool for races Dang has no stake in (builds, mirrors,
  deadlines), but the Dagger-specific aggregation happens in the engine,
  where it is atomic and edge-triggered. This also settles agent-messaging
  §9's `awaitAny` for code: subscribe a mailbox to the workers instead.
- **Capabilities.** `mailbox` mints a new handle, the same way `spawn` mints
  an agent. Code that holds a `Mailbox` (or a `MailboxID` passed as a tool
  argument, or bound into a foreman's tool object) can send to it. No global
  namespace is shared by name. `name` is only a label.
- **Lifetime.** Session-scoped, like agent runtimes (busybees §3.2 covers the
  cross-session durability that is still missing).

Alternatives rejected:

- *A "mailbox agent" with no model.* An `Agent` whose loop never runs, used
  just for its queue. It abuses the lifecycle states (always IDLE), and its
  messages would be consumed by turns that never happen.
- *`Agent.receive` on a real agent.* Code would steal messages from the
  agent's own turns. Agent messages are consumed at step boundaries
  (agent-messaging §2) and nowhere else.
- *Only `awaitAny` on agent lifecycle.* That covers settles but not human
  messages or clocks, and it is level-triggered (§2.4).

### 4.3 Blocking from inside an agent turn

agent-messaging §3: **"An agent's turn never blocks on another agent's
progress."** Code *may* block. `Agent.wait` and `.response` remain "for
callers that are NOT agent turns: … module code driving agents
imperatively". A Dang **tool function** called by an agent is running inside
that agent's turn, though. A `select`, `sleep` or `Mailbox.next` there holds
the turn open.

- **Engine side (enforced).** `Mailbox.next` called with an agent in the
  context (`AgentFromContext`) is **refused** with a message that teaches the
  fix: `an agent waits by ending its turn; subscribe it with
  notify(subscriber:) or schedule a message instead`. `Agent.wait` and
  `.response` keep their waits-for edges and cycle refusal (§4.5). A mailbox
  is not an agent, so it would add no edge, which is why it is refused
  outright.
- **Dang side (advisory).** Dang cannot tell it is inside a turn. The Dagger
  SDK glue would set a context value, `dang.ContextWithBlockingBudget(ctx,
  30*time.Second)`, for calls whose caller is an agent. `sleep`, `timeout`
  and `select` would then emit a warning, which shows in the trace, when a
  wait exceeds the budget. Warning, not refusing, because a 2-second backoff
  inside a tool is legitimate.

## 5. Worked examples

### 5.1 The factory run loop

With `select` plus today's Agent API, plus the `Mailbox` from §4:

```dang
type Factory {
  # … state as in busybees-as-modules §2.1 …

  """
  Run until told to stop: poll GitHub every 5 minutes, react to workers and
  messages the moment they arrive, and re-check scheduled follow-ups.
  """
  run(source: Workspace!): Void @cache(policy: Never) {
    let f = self
    let box = mailbox(name: "factory")       # humans/foreman send here (§4)
    let cursor = 0
    let nextPoll = Time.now                  # absolute deadline: poll at once
    let followUps = [] :: [FollowUp!]!       # FollowUp(at: Time!, issue: Int!)

    loop {
      # who is still working? (racing settled workers would win instantly, §2.4)
      let settled = [AgentState.IDLE, AgentState.FAILED, AgentState.STOPPED]
      let busy = f.workers.values.reject { w => settled.contains(w.state) }

      select {
        # the clock: an absolute deadline, so a chatty mailbox cannot starve it
        sleep(until: nextPoll) => {
          f = f.tick(source, full: true)     # gh poll → reconcile → dispatch
          nextPoll = Time.now.add("5m")
        }
        # a worker settled: cheap local pass, no GitHub cost
        w = busy.race { w => w.wait } if (!busy.isEmpty) => {
          f = f.settle(w).tick(source, full: false)
        }
        # the earliest scheduled follow-up came due
        due = followUps.race { u => sleep(until: u.at); u } if (!followUps.isEmpty) => {
          followUps = followUps.filter { u => u != due }
          f = f.recheck(due.issue)
        }
        # a human or the foreman said something (cursor read: never lost)
        m = box.next(after: cursor) => {
          cursor = m.seq
          case (m.text) {
            "stop" => break                  # breaks the loop, not the select
            "poll" => { nextPoll = Time.now }
            else => f.foreman.send(m.text)
          }
        }
      }

      # tick may ask for follow-ups ("re-check CI in 10m")
      followUps += f.takeFollowUps.map { u => FollowUp(at: Time.now.add(u.after), issue: u.issue) }
      f = f.clearFollowUps
    }
    null
  }
}
```

The version that uses only the mailbox, where workers are spawned with
`worker.notify(mailbox: box)` and follow-ups use `box.schedule(…)`, reduces
the `select` to two arms (`sleep(until: nextPoll)` and `box.next`). It could
even be one arm, `box.schedule("poll", after: "0s", every: "5m")`. Both
spellings are valid. The `select` version works across APIs, and the mailbox
version moves the aggregation into the engine.

### 5.2 Small examples

```dang
# Race two registries; the slower pull is cancelled.
let registry = select {
  container.from("docker.io/library/alpine:3.20").sync => "docker.io"
  container.from("mirror.gcr.io/library/alpine:3.20").sync => "mirror"
}

# Time-box a build. timeout yields null when the deadline wins.
let built = timeout("10m") { source.dockerBuild.sync }
if (built == null) {
  raise "build took longer than 10m"
}

# Wait for the first of N reviewers, but no longer than an hour.
let verdict = select {
  r = reviewers.race { r => r.send("review PR 57").response } => r
  sleep("1h") => "no review in time"
}
```

## 6. Implementation sketch (dang)

### 6.1 Grammar

The spike edits `pkg/dang/dang.peg` and regenerates it with
`go generate ./pkg/dang/`:

```peg
Form      <- Return / LegacyTryCatch / Raise / Conditional / Case / Select / Break / …
Select    <- SelectToken _ '{' _ (SelectArm Sep)* SelectArm? _ '}'
SelectArm <- ElseToken _ "=>" _ Form
           / Symbol _ '=' !'=' !'>' _ Form SelectCond? _ "=>" _ Form
           / Form SelectCond? _ "=>" _ Form
SelectCond <- _ IfToken _ '(' _ Form _ ')'
SelectToken <- "select" !WordChar
```

`select` is a contextual keyword, as `case` is (`WordToken`,
`dang.peg:558`, reserves only `null` and `rescue`). It still works as an
identifier, a field name (`obj.select`), and a call with parentheses. It
**shadows a user-defined `select { … }` block call**. That is a small
breaking change, so it belongs in the next Dang minor release, and in
Dagger's version gate it routes only modules with a new `engineVersion`
(`core/sdk/dang/README.md`). Follow-up work: regenerate tree-sitter
(`treesitter/generate.go`, which needs the `tree-sitter` CLI), and add the
editor keyword lists (the `editor-syntaxes` skill names three files) and LSP
keyword completion.

### 6.2 Type checker

`SelectExpr.Infer` (`pkg/dang/ast_select.go` in the spike):

- infer each guard behind `contextWithInferFunctionControlBoundary`;
- check `if` conditions against `Boolean!`;
- bind `binding: guardType` in a cloned env for the body;
- merge the body types with `mergeControlResultTypesTagged`;
- reject arms after `else`, and a `select` with no guards.

Still to do: the laziness lint (§2.3), a dedicated message for control flow
inside a guard, and flow narrowing from `if (cond)` into the body (as `if`
does with `analyzeCondition`).

### 6.3 Evaluator

`raceValues(ctx, n, fn) (idx, Value, error)` (`pkg/dang/stdlib_race.go`) is
the shared core of `select` and `.race`:

- start one goroutine per enabled arm, sending to a channel buffered to n;
- take the first result, then drain without blocking to prefer the lowest
  index among results already there;
- `cancel()`, then receive the remaining n−k results, so every loser has
  stopped before it returns.

`SelectExpr.Eval` evaluates conditions, calls `raceValues` with each guard
running in `scope.Derive(true)` under a context with no break or return
frames, then evaluates the winning body in `scope.Derive(false)` with the
binding bound. `sleep` is a `time.NewTimer` raced against `ctx.Done()` that
returns `context.Cause(ctx)`. `timeout` is `context.WithTimeout` plus a check
that turns only *its own* deadline into null. A parent cancellation, a real
error, or control flow passes through untouched.

When a GraphQL call is cancelled, the interpreter sees an error that wraps
`context.Canceled` coming out of `Execute`. `raceValues` discards losers'
errors, and `evalParallel` already prefers real errors over cancellation
noise (`eval.go:1704`). The engine-side effects are covered in §2.5.

### 6.4 Tests

These follow the repo's "Big Pile of Dang Scripts" convention (the `testing`
skill: `go test ./tests/`):

- `tests/test_select.dang`: the fast arm wins, binding, losers cancelled (a
  60s arm loses in milliseconds), timeout as a guard, `if` disabling, `else`,
  all-disabled raising, a raising guard, the loop idiom with body
  reassignment and `break`, a union result, and `return` from a body.
- `tests/test_race.dang`, `tests/test_sleep.dang`.
- `tests/errors/select_*.dang` with golden files (`-update`).
- To do: a Go unit test for `raceValues` under `testing/synctest`, as
  `parallel_selection_test.go` does for `evalParallel`, proving that losers
  are awaited. Also a gqlserver field that blocks until cancelled, to prove
  that cancellation reaches GraphQL requests.

### 6.5 Host hook for the trace (open)

Add a small interface in context, `dang.Instrumentation{ Span(ctx, name,
attrs) (ctx, end) }`, that `select`, `sleep` and `timeout` call. The Dagger
SDK glue would implement it with OTel, so a `select` shows as a parent span
with its guards' GraphQL spans beneath it, and the losers marked cancelled.
Without a host, nothing changes.

### 6.6 Sizing

| Piece | Effort |
|---|---|
| `Duration`/`Time` prelude scalars, `sleep(until:)`, `every` | 1–2 days |
| `select` from spike to production: tree-sitter, editors, LSP, laziness lint, messages, docs in the language reference | 2–3 days |
| `raceValues` synctest and cancellation test, gqlserver blocking field | ½ day |
| Dagger: `Mailbox` (cursor log, origin reuse), `notify(mailbox:)`, `schedule` on Agent and Mailbox, refusal from agent turns, integration tests | ~1 week |
| Dagger: bump `vito/dang/v2`, Dang SDK host hooks (`BlockingBudget`, `Instrumentation`) | 1–2 days |

## 7. Alternatives considered

- **Futures or promises as values** (`let a = async { … }; await a`). This
  creates work that outlives its expression, needs a new kind of type, and
  makes code harder to follow, which goes against "leaf in the wind". It is
  also unnecessary: `{{ }}` covers "all", and `select`/`.race` cover "first".
- **Channels and goroutines** (`go { }`, `chan`). The actors with identity
  are already Agents and Services. Bare goroutines in a glue language would
  bring back leaks and data races, while copy-on-write values currently make
  data races impossible to write.
- **Erlang's `receive … after`.** Elegant, but it only selects over a
  mailbox. Dang programs mostly race GraphQL calls. `select` with sleeping
  guards covers `after`, and `box.next` covers `receive`.
- **The `case` style `m: guard => body`.** In `case` and `rescue`,
  `x: Name =>` already means "type pattern". Using it for a binding would
  give one shape two meanings.
- **Go-style `case m := <-x:`.** `<-` and `:=` are new tokens, and `case`
  inside `select` would clash with Dang's `case` expression.
- **A `race(&a, &b)` builtin.** Not expressible (§2.6).
- **An engine-side `Query.sleep`.** Discussed in §1. Optionally the Dagger
  SDK could add it later as a trace-visible alias, but the language does not
  need it.
- **Random fairness.** Possible, but it makes tests nondeterministic in the
  *ready-at-once* case. The spike's best-effort lowest-index preference
  costs nothing and promises nothing.

## 8. Open questions

1. **Random or biased fairness?** Go randomizes to avoid starvation. This
   proposal argues absolute deadlines make that unnecessary. Is it worth
   adding a `select biased { }` modifier, or randomization by default?
2. **`timeout` returning null or raising.** Null fits the "expected
   absence" guidance. A `TimeoutError` would be easier to `rescue` in deep
   call chains. Should both exist (`timeout` and `deadline`)?
3. **Side-effecting guards.** Should Dang warn when a guard calls a mutation
   (`Mutation.*` fields are known), as a weak signal that it breaks §2.4?
4. **Durable mailboxes and schedules** across sessions (busybees §3.2). The
   cursor design suits persistence, since the log plus cursor can be
   restored, but this proposal stays session-scoped.
5. **`every` or a ticker value.** A first-class `Ticker` (`let t =
   ticker("5m")` then `t.next` as a guard) would make the absolute-deadline
   idiom implicit. It is stateful, though, which is awkward in a
   copy-on-write language. `sleep(until:)` is the minimal form.
6. **Engine cancellation semantics** for `withExec` and other BuildKit work
   when a guard loses: abort it, or let it finish for the cache? Today this
   depends on whether other callers share the work.

## Spike

Commits on this branch, none of them pushed:

| Commit | Contents |
|---|---|
| `stdlib: spike sleep and timeout builtins` | `sleep(duration: String!)`, `timeout(duration) { }`, `loop` checks for cancellation; `tests/test_sleep.dang` |
| `stdlib: spike [T]!.race` | `xs.race { x => … }`, `raceValues`; `tests/test_race.dang` |
| `lang: spike the select expression` | grammar, `SelectExpr` (infer/eval), formatter; `tests/test_select.dang`, three error goldens |

Differences from this proposal: durations are Go strings, not a `Duration`
scalar; there is no `sleep(until:)`, `Time` or `every`; the spike has no
laziness lint, no tree-sitter or editor support, and no host hooks.
`go test ./...` passes; the editor and LSP Neovim tests are skipped because
their submodules are not checked out.
