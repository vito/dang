# Proposal: structured concurrency with task values

Status: draft, with a working spike on this branch (§6). Competes with
`docs/proposals/concurrency-and-time.md` (the "incumbent": a `select { }`
expression, `sleep`/`timeout`/`.race`, and an engine `Mailbox`).

## 1. Core idea and who finds it familiar

**`async { … }` starts a `Task[T]`. Every task belongs to a scope that
cancels and awaits it on exit. Waiting for "first of", "all of", or "with a
deadline" are ordinary methods on tasks.** Nothing new is syntax.

```dang
let t = async { source.dockerBuild.sync }          # Task[Container!]!
let ref = [pull(a), pull(b)].race                   # first wins, loser cancelled
let done = workers.map { w => async { w.wait } }.awaitAny   # the others stay in flight
let built = t.timeout("10m")                        # null if the deadline wins
```

This is the substrate the incumbent's `select` is sugar for: tokio's
`select!` is a macro over futures. The incumbent adopts the sugar and rejects
the substrate. This proposal adopts the substrate, which makes the sugar
optional.

**Familiar to:** JS/TS (`async`/`await`, `Promise.race`/`Promise.all`),
Python (`asyncio.TaskGroup`, `wait(FIRST_COMPLETED)`, `as_completed`), Kotlin
(`async`/`await` inside `coroutineScope`), Swift (`async let`, task groups),
Rust (`JoinSet`), Java (`StructuredTaskScope`), Trio (nurseries), and Go
programmers who use `errgroup`. This covers most Dagger SDK users, who
already write `Promise.all`/`errgroup` in their TS/Go modules.

**The incumbent's objection** (§7 there): futures "create work that outlives
its expression, need a new kind of type, are harder to follow, and are
unnecessary". Structured concurrency is the answer to the first point, and the
spike answers the second:

- *Outlives its expression.* A task outlives its *expression* the way a
  `let` binding does, and no longer than its *scope*. When a scope exits, it
  cancels every task that is still running and waits for all of them to
  unwind (`taskScope.close`, `pkg/dang/stdlib_task.go:121`). The program,
  each module-function call, each `tasks { }` block, and each task body is a
  scope. No goroutine survives the call that started it, which is the
  incumbent's own rule 2. Long-lived actors are still `Agent`s.
- *A new kind of type.* `Task[T]` is a one-parameter type exactly like
  `Map[T]` (`TaskType`, `pkg/dang/types.go:248`). It unifies structurally
  with no new unifier code (`hm/unify.go:66–103`) and is written in
  signatures with the existing `Name[T]` syntax (`types.go:86`).
- *Unnecessary.* `select` cannot keep a wait in flight across loop
  iterations, cannot be passed to a helper, and cannot express "as results
  arrive". §3.5 and §5 show why the factory needs all three.

**Incumbent §2.6, "a builtin cannot race expressions"**, is answered point
by point:

| §2.6 obstacle | Answer |
|---|---|
| Arguments are evaluated eagerly (`ast_expressions.go:329`) | The *block* is lazy. `async` is a block-taking builtin, and the block becomes a `FunctionValue` closure (`BlockArg.Eval`, `ast_expressions.go:3384`) that the builtin runs on a goroutine. |
| One block per call | One block per *task*. Racing N things means N `async` calls in a list, so arity is unlimited and dynamic. |
| `{{ }}` waits for all | Not used. |
| Arm bodies must run in the caller's control flow | `race` *returns a value*, and an ordinary `case` dispatches on it in the caller's frame, so `break`/`return` work unchanged. |

## 2. Surface

No grammar change. All of these are `registerTasks()` builtins
(`pkg/dang/stdlib_task.go:243`), declared with the stock DSL.

| Builtin | Type | Meaning |
|---|---|---|
| `async { b }` | `Task[a]!` | Start `b` concurrently in the innermost scope. |
| `tasks { b }` | `a` | Run `b` in a new scope that cancels and awaits its tasks when `b` exits. |
| `sleep(d)` | `null` | Wait. Cancellable. (`Duration` scalar as in the incumbent §3.1.) |
| `t.await` | `a` | Wait for the value, or raise the task's error. |
| `t.timeout(d)` | `a` (nullable) | Like `await`, but on the deadline cancel `t` and yield `null`. |
| `t.cancel` | `null` | Cancel `t` and wait for it to unwind. |
| `t.isDone` | `Boolean!` | Non-blocking check. |
| `ts.race` | `t` | First to finish wins (value *or* error). Cancel and await the rest. |
| `ts.awaitAny` | `Task[t]!` | Return the first finished **task**. The rest keep running. |
| `ts.awaitAll` | `[t]!` | All values in order. The first failure cancels the rest and raises. |
| `ts.eachCompleted { v => … }` | `null` | Visit values in completion order. `break` stops early. |

(`ts: [Task[t]!]!`. The list methods read the element's result type through
a `'t'` type variable in `instantiateListMethod`, `ast_expressions.go:3451`.)

Tasks are **values**. They can be stored in lists and maps, returned from
helpers, and compared by identity (`valuesEqual`, `pkg/dang/ast.go:309`).
Combinators are therefore user-definable: `awaitAny` plus `filter` is enough
to write `firstOk`, a quorum, or a retry with backoff in plain Dang (§5b).

**Heterogeneous races** use a declared union. `TaskType.Supertypes` lifts the
supertypes of the result type (`types.go:294`), and object types count their
unions as supertypes (`env.go:1253`). So `[async { Tick() }, async { Msg() }]`
infers as `[Task[Wake!]!]!` with `union Wake = Tick | Msg`. The spike test
checks this (`tests/test_tasks.dang`, the `Wake` loop).

**Dagger API: taken from the incumbent, minus one item.** Keep `Mailbox`
with cursor reads (`mailbox`, `send`, `next(after:)`, `messages(after:)`),
`notify(mailbox:)`, and `Agent.schedule`. The incumbent's arguments for these
(§4.2) hold unchanged: only the engine can own state that outlives a call.
Drop `Mailbox.schedule`, because in-process follow-ups are sleeping tasks
(`async { sleep("10m"); Due(42) }`) that can be cancelled by handle. Keep
`Agent.schedule`, which still has to wake a *model* that has no code
running. No `Query.sleep`.

## 3. Semantics

### 3.1 Scopes, lifetime, cancellation

- **Ownership is dynamic.** `async` attaches the task to the innermost scope
  carried in `ctx` (`taskScopeFrom`). That scope is not necessarily the
  lexical function. A helper such as `pull(ref): Task[Container!]!` returns a
  task owned by its *caller's* scope (Kotlin's `CoroutineScope` receiver,
  passed implicitly the way `ctx` already is). This is the composability
  lexical `select` lacks, and it stays structured: the owning scope is always
  on the stack.
- **Root scopes.** `RunFile` (`eval.go:2487`) and `RunDir` (`eval.go:2809`)
  open one around evaluation, mirroring the existing `ensureServiceRegistry`
  plus `defer StopAll` pattern (`eval.go:2429`). The Dagger SDK opens one per
  module-function call in `callDangFunction`
  (`core/sdk/dang/v2/helpers.go:417`, dagger repo). **Nothing outlives the
  GraphQL call.**
- **Exit cancels; it does not join forever.** At scope exit, unfinished tasks
  are cancelled with a cause ("its scope exited"), then awaited. This is
  Swift's `async let` rule. Joining instead (Trio/Kotlin) would let a
  forgotten `async { sleep("1h") }` hang a function. A lint flags tasks that
  are never awaited (§6).
- **Each task body is itself a scope.** Its children are joined before it
  reports done (`spawn`, `stdlib_task.go:141`), so `timeout` on a task that
  fanned out cancels the whole subtree.
- **Cancellation is the context.** A task's context keeps the values from its
  call site (stdout, services, imports) but takes cancellation from its scope
  (`context.WithoutCancel` plus `context.AfterFunc`). In Dagger, a cancelled
  task aborts its in-flight HTTP request, and the engine's waits return
  `context.Cause` without side effects (`WaitSettled`, `core/agent.go:2457`).
  The causes are distinct (`errTaskRaceLost`, `errTaskTimedOut`,
  `errTaskScopeExited`, all wrapping `context.Canceled`), which feeds the
  trace (§3.6).
- **Isolation.** A task body runs in a *sealed* child scope with its own
  `self` cell (`async` impl, `stdlib_task.go:244`), just as a `{{ }}` field
  does (`block.go:600`). Writes stay private. Results come back only through
  `await`, so copy-on-write makes data races unwritable, as the incumbent
  wants.
- **Control flow cannot leave a task.** `async` clears the block's captured
  `return`/`break` frames, so `break` inside a task raises at runtime.
  Production rejects it at type-check time (§6).

### 3.2 Atomicity: cancellation points are named

The incumbent's §2.4 rule ("guards observe; they don't consume") exists
because *every* losing `select` guard is cancelled on *every* evaluation. With
tasks, **losing does not imply cancelling.** Only four operations cancel a
task: `race`, `timeout`, `cancel`, and scope exit. The factory loop uses
`awaitAny`, which cancels nothing. So during the loop a `box.next` that loses
to a tick is not abandoned mid-flight. It keeps waiting and wins on a later
iteration. Even a destructive `receive` would be safe in that loop. The rule
becomes **"only cancel tasks that observe,"** and every cancellation site is a
named call a reviewer can grep for.

We still adopt the cursor mailbox, because scope exit does cancel, and because
idempotent reads are the right engine design anyway. Can the types help? Only
weakly. Dagger exposes everything under `Query`, so Dang has no effect
information. A schema directive (`@observes`) could let `race` and `timeout`
warn on non-observing tails (§8).

### 3.3 Failure

- **Errors are values until awaited.** A task's error is raised at `await`
  and can be rescued there: `t.await rescue fallback`. For per-task handling,
  rescue inside the block: `async { pull(x) rescue null }`.
- **The combinators are fail-fast.** In `race`, the first *finisher* wins even
  if it raised, as in the incumbent's `select`. `awaitAll` cancels its
  siblings on the first failure, as `evalParallel` does (`eval.go:1661`).
- **No silent errors.** If a task fails and nobody observed it (no `await`,
  `race`, …), its error is raised when the scope exits normally
  (`taskScope.close`). Errors caused by cancellation never count. This is
  Trio's guarantee; the spike tests it.

### 3.4 Typing

`async: ({} -> a) -> Task[a]!` is an ordinary polymorphic builtin. The list
combinators substitute `'t'` := X for a `[Task[X]]` receiver. Annotations
work (`jobs: Map[Task[Wake!]!]!`). Unions come from declared unions (§2).
A list literal does **not** widen to an ad-hoc union (`List.Infer` uses
`hm.MergeTypes`, `ast_literals.go:74`), so heterogeneous races need wrapper
types. See §7.

### 3.5 Fairness

When tasks have **already finished**, they win in list order (`firstDone`,
`stdlib_task.go:217`). That is deterministic, so list order is priority, and
tests of the ready-at-once case are reproducible. The incumbent picked lowest
index on a best-effort basis and saw `"c"` win in its spike. Among tasks still
running, the scheduler decides. **Timer-reset starvation cannot happen**:
the incumbent needs absolute `sleep(until:)` deadlines because `select`
rebuilds every guard on every iteration, so a chatty mailbox keeps restarting
`sleep("5m")`. A clock *task* is created once per period and stays in flight
while other wakes are handled. Its deadline is absolute by construction.

### 3.6 Value semantics, the boundary, restore, the trace

- A `TaskValue` is a handle, like `GraphQLValue`. Copying it aliases the
  task, and copy-on-write objects that hold one copy the handle. It refuses to
  marshal (`stdlib_task.go:91`).
- **It may not cross the GraphQL boundary.** `dangTypeToTypeDef` already
  rejects `Map` there (`core/sdk/dang/v2/helpers.go:1119`). `Task` gets the
  same `case`, extended to the field types of module objects, since private
  state is serialized too. A task in a module field could never be resumed in
  a later call, so this is a static error rather than a runtime surprise.
- **Restore / ID replay:** tasks are runtime-only. A replayed `run` restarts
  from the top. This is identical to the incumbent: durable state lives in the
  engine (mailbox log, agents) and on GitHub.
- **Inside an agent turn:** adopt the incumbent's §4.3 unchanged (refuse
  `Mailbox.next` from a turn, warn on long blocking). Tasks add one guarantee:
  a tool function cannot leave background work behind, because its scope
  ends with the call.
- **Trace:** each task is a span. With the incumbent's host hook
  (`dang.Instrumentation`, its §6.5), `async` opens a span named for its site
  (`async factory.dang:42`) under the span that was active at spawn. Its
  GraphQL calls nest beneath it, and it ends as ok, error, or cancelled with
  its cause ("lost a race", "timed out", "scope exited"). This is richer than
  a `select` span, because a task that stays in flight across iterations is
  one long span, which matches what actually happened.

## 4. Layering

| Layer | What | Why |
|---|---|---|
| Language | **Nothing.** `Task[T]` is a built-in generic like `Map[T]`, and blocks already exist. Prerequisite fix: type-pattern `case` clauses must be able to update outer bindings (§6). | The design is a library. |
| Dang stdlib | `async`, `tasks`, `sleep`, `Duration`/`Time`, the `Task` methods, the list combinators | Engine-agnostic. Cancellation goes through `ctx` exactly. |
| Dang host hooks | `ContextWithTaskScope` (spike, exported), `Instrumentation` (shared with the incumbent) | So hosts own the root scope and the spans. |
| Dagger API | `Mailbox` + cursor, `notify(mailbox:)`, `Agent.schedule` (from the incumbent) | State that outlives a call. |

**Non-Dang SDKs** gain the engine pieces, as they do in the incumbent. Their
languages already have tasks (goroutines plus errgroup, `Promise`,
`asyncio.TaskGroup`), so this proposal makes a Dang module read like the same
program written in Go or TS, and porting between SDKs is mechanical. A
`select` keyword has no counterpart in TS or Python.

## 5. Worked examples

### (a) The factory run loop

Every wait is a task that stays in flight until it fires. Each dispatched job
is one task, and an `AgentMessage` already *is* a future on the engine side
(`send` returns a handle and `response` awaits it, `core/schema/agent.go:74`
and `:174`). Follow-ups are sleeping tasks.

```dang
type Poll { full: Boolean! }
type Settled { key: String! }
type Said { m: MailboxMessage! }
type Due { issue: Int! }
union Wake = Poll | Settled | Said | Due

run(source: Workspace!): Void @cache(policy: Never) {
  let f = self
  let box = mailbox(name: "factory")                   # §2: from the incumbent
  let cursor = 0
  let clock = async { Poll(full: true) }               # poll at once
  let inbox = async { Said(box.next(after: cursor)) }  # cursor read: cancel-safe
  let jobs = [:] :: Map[Task[Wake!]!]!                 # worker key -> its job
  let later = [] :: [Task[Wake!]!]!                    # follow-ups: sleeping tasks

  loop {
    # list order is priority; nothing here is cancelled, losers stay in flight
    let fired = ([clock, inbox] + jobs.values + later).awaitAny
    later = later.filter { t => t != fired }
    case (fired.await) {
      p: Poll => {
        f = f.tick(source, full: true)                 # gh poll -> reconcile
        clock = async { sleep("5m"); Poll(full: true) }
      }
      s: Settled => {
        jobs = jobs.without(s.key)
        f = f.settle(s.key).tick(source, full: false)  # local pass, no GitHub
      }
      d: Due => { f = f.recheck(d.issue) }
      m: Said => {
        cursor = m.m.seq
        inbox = async { Said(box.next(after: cursor)) }
        case (m.m.text) {
          "stop" => break                              # scope exit cancels all
          "poll" => { clock.cancel; clock = async { Poll(full: true) } }
          else => f.foreman.send(m.m.text)
        }
      }
    }
    # tick decided what to start: one task per job, done when its turn ends
    f.dispatches.each { d =>
      jobs = jobs.with(d.key, async { d.worker.send(d.brief).response; Settled(d.key) })
    }
    later += f.followUps.map { u => async { sleep(u.after); Due(u.issue) } }
    f = f.clearPlans
  }
  null
}
```

Compared with the incumbent's loop (its §5.1): no `busy` filter is needed to
avoid spinning on a level-triggered `Agent.wait`, because a job task fires
exactly once, when *that* message's turn ends. No `sleep(until:)`
bookkeeping is needed. N in-flight waits are issued once, not once per wake.
The incumbent re-issues `busy.race { w.wait }` (N HTTP long-polls) and
re-sorts `followUps` on every iteration. Cancelling one follow-up is
`t.cancel`.

### (b) Race two registries, and a user-defined combinator

```dang
pull(ref: String!): Task[Container!]! { async { container.from(ref).sync } }

let alpine = [pull("docker.io/library/alpine:3.20"),
              pull("mirror.gcr.io/library/alpine:3.20")].race

# "first that SUCCEEDS" (race takes the first that FINISHES): plain Dang
firstOk(ts: [Task[Container!]!]!): Container! {
  let pending = ts
  loop {
    let t = pending.awaitAny
    pending = pending.filter { p => p != t }
    let c = t.await rescue null
    if (c != null) { pending.each { p => p.cancel }; break c }
    if (pending.isEmpty) { raise "every mirror failed" }
  }
}
let img = firstOk(config.mirrors.map { m => pull(m + "/alpine:3.20") })
```

### (c) Time-box a build

```dang
let built = async { source.dockerBuild.sync }.timeout("10m")
if (built == null) { raise "build took longer than 10m" }

# a deadline over a whole fan-out: the subtree is cancelled together
let tested = async { platforms.map { p => async { test(p).sync } }.awaitAll }.timeout("30m")
```

### (d) First of N reviewers, capped at one hour

```dang
let verdict = async {
  reviewers.map { r => async { r.send("review PR 57").response } }.race
}.timeout("1h") ?? "no review in time"

# or: use every review that arrives within the hour, as it arrives
let reviews = [] :: [String!]!
async {
  reviewers.map { r => async { r.send("review PR 57").response } }
    .eachCompleted { v => reviews += [v]; if (reviews.length == 2) { break } }
}.timeout("1h")
```

The inner tasks are children of the outer one, so the deadline cancels every
outstanding reviewer wait (the agents themselves keep running). In the
second form, writes to `reviews` inside a task are private (§3.1). The real
version returns the list from the block instead. See §7.

## 6. Implementation sketch and sizing

**Spike, already on this branch** (`go test ./pkg/... ./tests/` passes):

| Commit | Contents |
|---|---|
| `stdlib: spike Task values and structured scopes` | `TaskType` (`types.go`, +63 lines), `Task` method dispatch in `Select.Infer`/`Eval` (+35), the `'t'` projection in `instantiateListMethod`, identity equality, root scopes in `RunFile`/`RunDir`, `stdlib_task.go` (scope, spawn, all builtins, ~480 lines with docs and examples), `tests/test_tasks.dang` (concurrency, race cancels the 1h loser, timeout, error routes, scope-exit cancel, unobserved-failure raise, private writes, helper-returned tasks, `awaitAny` loop, `eachCompleted` plus `break`, a union-typed race driving a loop) |
| `case: let type-pattern clauses update outer state` | `ast_patterns.go:379` derived a **sealed** scope for type-pattern clauses, so `n += 1` inside `t: Tick => { … }` was silently lost, while the same write in a value clause (`:395`) was not. This is a pre-existing bug that any `case`-dispatch loop hits, and it is a one-line fix plus `tests/test_case_type_pattern_writes.dang`. (`rescue` clauses do the same, `ast_errors.go:270–285`, see §8.) |
| `tests: cover the factory loop shape with tasks` | Example (a) without GraphQL: a persistent clock, a `Map` of job tasks, follow-ups, and `awaitAny` (events arrive in deadline order). Also list-order priority among finished tasks, and `firstOk` from (b). |

**To production (Dang): ~3 days.**
- Static rejection of `break`/`continue`/`return` in `async` blocks: a
  `DetachedBlock()` flag on `BuiltinDef`, making `FunCall.Infer` infer the
  block under `contextWithInferFunctionControlBoundary`
  (`ast_expressions.go:187`, `control_flow.go:135`). ½ day.
- A type error when `race` and friends get a non-task list (today `'t'`
  stays free and the call fails at runtime). ½ day.
- Lints: "task never awaited" (an unused-binding walk) and "a lazy Container
  makes this task finish immediately", reusing `tailLeavesLazyHandle`
  (`rescue_analysis.go:459`). 1 day.
- `Duration`/`Time` prelude scalars (the incumbent's §3.1, as is), LSP
  completion for `Task` (`complete.go:219`, like `MapType`), the
  `module_format`/`union_provenance` switches, and docs. 1 day.
- No tree-sitter, editor keyword, or formatter work, because there is no
  syntax.

**Dagger: ~1 week, nearly all of it shared with the incumbent.** Wrap
`callDangFunction` in `ContextWithTaskScope` (helpers.go:417) and reject
`TaskType` in `dangTypeToTypeDef` and object fields (helpers.go:1119): ½ day.
`Mailbox`, `notify(mailbox:)` and `Agent.schedule`: ~1 week, as the
incumbent sizes them minus `Mailbox.schedule`. `Instrumentation` spans: 1 day.

## 7. Honest weaknesses and what the incumbent does better

- **Heterogeneous races need wrapper types.** `select { m = box.next(…) =>
  … }` binds each arm's own type inline. Here every source needs an object
  type and a union member (`Poll`, `Settled`, …) because unions only admit
  object types and list literals don't widen. For a two-way race that is
  real ceremony. A fix would be to let list literals of tasks widen to ad-hoc
  unions, as control-flow merges already do (`mergeControlResultTypesTagged`,
  `union_provenance.go:40`), or to build `select` later as sugar over tasks.
- **Bookkeeping is explicit.** The loop maintains `jobs`, `later`, and the
  identity filter itself. `select` hides "which guard was this" behind
  syntax. In exchange the program can *keep* tasks, but it is more state to
  get wrong (forgetting to re-arm `inbox` silently stops message handling).
- **Reference identity enters a copy-on-write language.** A `TaskValue` is
  the first non-GraphQL value with identity semantics. It is contained (no
  boundary crossing, no marshaling), but it is a new idea for Dang users.
- **Reads are live.** A sealed scope isolates a task's writes, not its reads.
  A task reads enclosing bindings when it runs, not when it was spawned.
  `{{ }}` has the same property today, but tasks live longer, so it matters
  more (§8).
- **Private writes surprise.** Example (d)'s `reviews += [v]` inside a task
  does not reach the outer binding. That is correct (no data races), but it
  will surprise people. Return values instead.
- **Forgetting `.await` cancels silently** at scope exit unless the lint
  fires. `select` cannot be forgotten.
- **Two ways to wait for "first".** `race` cancels and `awaitAny` doesn't.
  That is honest about cancellation (§3.2), but it is one more choice to make.
- **What the incumbent does better:** a static, small race reads top to
  bottom with the handling inline. Arm `if (cond)` guards are syntax. It is
  tokio-familiar. There is nothing to manage. For (b), (c) and (d) the two
  designs are about equally short. The factory loop (a), with its dynamic job
  set and its follow-ups, is where tasks pull ahead, and that loop is the
  stated motivating problem.

## 8. Open questions

1. **Implicit versus explicit nursery.** The spike's scope is dynamic
   (`ctx`). The Trio purist form, `tasks { s => s.async { … } }`, makes
   ownership visible but costs a parameter threaded through every helper.
   Kotlin chose implicit. Is that right for Dang?
2. **Cancel or join at scope exit.** This proposal picks cancel (Swift
   `async let`). Should `tasks(join: true) { }` exist for fire-and-collect?
3. **Snapshot captures.** Should `async` copy the bindings its block reads at
   spawn time (cheap under copy-on-write) to fix the live-read issue in §7?
4. **Ad-hoc union widening** for list literals of tasks, or `select` as
   sugar over `awaitAny` plus `case`, to remove the wrapper types.
5. **`@observes` schema directive** so `race`/`timeout` can warn about
   cancelling consuming calls (§3.2).
6. **`rescue` clauses also run sealed** (`ast_errors.go:270–285`). Should
   they get the same fix as `case`?
7. **Durable follow-ups.** Sleeping tasks die with the call. Should a
   follow-up that must survive a restart be `box.schedule` after all?
