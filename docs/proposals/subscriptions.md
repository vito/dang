# Proposal: GraphQL subscriptions as streams

Status: draft, with an implementation on this branch (§6).

A long-running module function has to wake **at once** when something
happens elsewhere: an agent settles, a message arrives, a timer fires. Code
may block, and it needs something to block *on*. GraphQL already has a name
for an event source: a field on the `Subscription` root. Dang reads one as a
stream.

## 1. Core idea

**A subscription root field is a cold stream of its events, and events are
read exactly like any other GraphQL result: you select what you want.**

```dang
Subscription.mailboxEvents(mailbox: box, after: cursor).{{
  ... on AgentEvent {{ seq, agent, state }}
  ... on MessageEvent {{ seq, text }}
}}.each { ev =>
  case (ev) {
    a: AgentEvent => f = f.settle(a.agent)
    m: MessageEvent => f.foreman.send(m.text)
  }
}
```

Naming the field builds a recipe; nothing is sent until a terminal consumes
it. The `.{{ }}` selection *is* the operation's selection set, so every
event arrives already shaped. There is one loop, `.each`, left with `break`,
plus `.first`, `.toList` and `.take(n)`.

The design adds no syntax and no new way to read a GraphQL object. It adds
one generic type, four builtin methods, a subscription operation kind in the
query builder, and a graphql-sse transport. Ordering, durability, fan-in and
cursors stay in the engine, behind ordinary schema fields.

## 2. Surface

### 2.1 Schema

Whatever subscription root fields the engine exposes. Dang reads them from
introspection (`subscriptionType`). For the agent factory the engine is
converging on an append-only, cursor-addressed log:

```graphql
type Subscription {
  agentEvents(agent: AgentID!, after: Int): AgentEvent!
  mailboxEvents(mailbox: MailboxID!, after: Int): MailboxEvent!
}
```

### 2.2 Dang

| Kind | Form | Meaning |
|---|---|---|
| source | `Subscription.field(args)` | The stream of a subscription root field's events. Fields live in a `Subscription` namespace, as mutation fields live in `Mutation`. |
| select | `stream.{{ fields }}` | **The way in for object events.** Becomes the operation's selection set; each event is the selected record: `Stream[{seq, text}!]!`. Aliases and nested selections work as on a query. |
| select | `stream.{{ ... on A {{ … }} ... on B {{ … }} }}` | Per-member selection on an interface- or union-typed field. Each event is a member of the narrowed union, so `case` type patterns dispatch on it. |
| type | `Stream[a]` | A cold sequence of `a`. Writable in type position: `let s: Stream[Int!]! = …`. |
| terminal | `.each { ev => … }` | **The loop.** Completes with `null`; `break v` yields `v`; `continue` and `return` work as in a list's `.each`. |
| terminal | `.first` | The first value, or `null` if the stream completes empty. |
| terminal | `.toList` | Every value, once the stream completes. |
| bound | `.take(n)` | A stream that completes after `n` values, cancelling its source. `.take(n).toList` is the bounded collect. |
| source | `xs.toStream` | A stream over a list's elements, for tests and fakes. |

A subscription field of **leaf type** (a scalar, an enum, or a list of them)
is a plain `Stream[T]!` and can be consumed directly. A field of **object
type** cannot: a pushed event carries only what the operation selected and
cannot be re-queried afterwards, so, exactly as a GraphQL list of objects
must be selected before it is iterated, the stream must be selected before
it is consumed. Two compile errors enforce it:

```
Error: cannot call stream method "each" directly on a stream of GraphQL
objects; select the fields each event should carry first, e.g.
stream.{{id}}.each
```

```
Error: inline fragment on MessageEvent selects no fields: a pushed event
cannot be re-addressed later, so select what each event should carry,
e.g. ... on MessageEvent {{ id }}
```

and one more rejects fanning out over the namespace
(`Subscription.{{a, b}}`): a subscription operation selects exactly one root
field.

Wanting a handle to the event means selecting `id`. On a schema whose events
are nodes with typed IDs (`id: AgentEventID!`), the selected `id` is loaded
as a handle by the ordinary ID rule, and the rest of the object is one query
away; Dang adds nothing for it.

## 3. Semantics

### 3.1 Cold

`Subscription.events(after: c).{{seq}}` evaluates its arguments and returns
a `StreamValue` holding the `subscription { events(after: c) { seq } }`
operation. Nothing is sent. Each terminal opens the stream afresh, so
consuming a stream twice subscribes twice. `let s = …; s.first; s.first`
sees the same first event twice; this is the contract of every lazy GraphQL
handle in Dang.

### 3.2 Control flow

Terminal blocks run synchronously on the caller's goroutine, inside the
terminal's call frame. `break v`, `continue` and `return` behave exactly as
in a list's `.each`, and writes to enclosing bindings go through. The body
never runs concurrently with itself, so state folded in the loop needs no
locking.

### 3.3 Cancellation and lifetime

A terminal opens the stream under a child context and, **whatever route it
exits by** (completion, `break`, `return`, `raise`, `.first` having its
value, `.take(n)` reaching `n`), cancels that context and closes the
response body before returning. Closing the request is how graphql-sse
cancels a subscription, so the engine's resolver context is cancelled too.
The test server counts running subscription resolvers, and the tests assert
the count drains to zero after breaking out of a stream that would otherwise
stay open for a minute.

There is no goroutine on the Dang side: the SSE body is read on the caller's
goroutine, so a stream cannot outlive the expression consuming it.

### 3.4 Failure

- A `next` event carrying `errors` raises a `GraphQLError` from the
  terminal, with the server's message, path and extensions. Events already
  handled stay handled. The same applies when the server rejects the
  operation up front, or answers a plain JSON error result instead of a
  stream (a server without subscription support).
- A non-200 response raises its GraphQL errors if the body has them, or the
  status and body otherwise.
- `complete` ends the stream normally: `.each` returns `null`, `.toList`
  returns what it has, `.first` returns `null`.
- A connection that ends **without** `complete` raises (`subscription stream
  ended without a complete event`), so a dropped connection is never
  mistaken for a finished log.
- If the body raises, the terminal raises and the subscription is cancelled.

### 3.5 Delivery and resumption

A subscription connection is at-most-once and lives exactly as long as its
request. Resuming is **the caller's job**, through whatever cursor argument
the field takes (`after:`). Advance the cursor only after an event has been
handled, so a connection that drops resumes at the first unhandled event:
at-least-once handling over at-most-once connections, with no client-side
buffer. The cursor is plain data, so a restored function that persisted it
resumes where it stopped.

```dang
let cursor = f.cursor
loop {
  Subscription.mailboxEvents(mailbox: box, after: cursor).{{
    ... on AgentEvent {{ seq, agent }}
    ... on MessageEvent {{ seq, text }}
  }}.each { ev =>
    f = f.handle(ev)
    cursor = case (ev) {
      a: AgentEvent => a.seq
      m: MessageEvent => m.seq
    }
  } rescue { e: Error =>
    print("mailbox stream dropped, resuming: " + e.message)
  }
}
```

Two things the idiom has to get right: a normal `complete` means the log is
closed (the loop above never sees one from a live mailbox), and a handler
that raises on event *k* leaves the cursor at *k-1*, so the same event is
redelivered on reconnect. Whether to skip a poisoned event or stop is the
handler's decision, not the stream's.

### 3.6 Backpressure

The terminal reads one event per pull, after the body has finished the
previous one. While the body runs, events queue in the TCP connection and
then in the engine. How much the engine buffers per subscriber, and whether
a slow subscriber is dropped or blocked, is the server's policy.

### 3.7 Level-triggered sources

A field that starts with the *current* state (the engine's `agentEvents`
without `after:` replays the transition into the agent's current state
first) makes `.first` answer "what is the state now", not "what changes
next". To wait for a change, pass `after:` the current seq.

## 4. Layering

| Layer | What |
|---|---|
| Grammar | **nothing** |
| Type system | `Stream[a]`; the unselected `GraphQLStreamType`, the stream twin of `GraphQLListType` |
| Stdlib | `each`, `first`, `toList`, `take`, `List.toStream` |
| GraphQL layer | subscription operations (`querybuilder.Subscription`, `Subscribe`); the graphql-sse client (`pkg/gqlsse`); `.{{ }}` on a subscription builds the selection set |
| Dagger API | `Subscription` root fields, the event log and its cursors, edge-triggering, refusal inside agent turns |

"Dang knows no Dagger" holds: any graphql-sse server works, as the test
server shows.

## 5. Examples

### (a) Wait for one agent to settle

```dang
let settled = Subscription.agentEvents(agent: worker, after: worker.seq).{{state}}.first
case (settled.state) {
  AgentState.FAILED => raise "worker failed"
  else => worker.collect
}
```

### (b) Tail a stream, bounded

```dang
let firstThree = Subscription.events.{{seq}}.take(3).toList
```

### (c) A leaf-typed field needs no selection

```dang
Subscription.heartbeat(every: "1s").each { t => print(t) }
```

## 6. Implementation

- `pkg/dang/types.go`: `StreamType`, `GraphQLStreamType`; `Stream[...]` in
  type position.
- `pkg/dang/stream.go`: `StreamValue`, `consume` (open under a child
  context; cancel and close on every exit), `take`, `listStream`, and
  `.{{ }}` on a stream.
- `pkg/dang/stream_graphql.go`: `subscriptionFieldType`,
  `newSubscriptionStream`, `selectEvents` (fields or inline fragments
  become the operation's selection set).
- `pkg/dang/stdlib_stream.go`: the builtins, each with a runnable example.
- `pkg/dang/ast_expressions.go`: `Select.Infer` rejects methods on a
  `GraphQLStreamType`; `ObjectSelection.Infer`/`Eval` on streams.
- `pkg/querybuilder/subscription.go`: `Subscription()`, `Subscribe(ctx)`,
  `EventStream.Next`.
- `pkg/gqlsse/client.go`: `Subscriber`/`Stream` and `Client`, which wraps
  genqlient's client for queries. Dang type-asserts its `graphql.Client` to
  `gqlsse.Subscriber`; the lazy `dang.toml` service client delegates.

### 6.1 Wire protocol (graphql-sse, distinct connections mode)

**Request.** `POST` to the query endpoint with `Content-Type:
application/json` and `Accept: text/event-stream`, plus whatever the
configured transport adds. The body is the ordinary request JSON; arguments
are inlined as literals:

```json
{"query":"subscription Subscription {ticks(n:3){n label}}","operationName":"Subscription"}
```

**Response.** `200` with `Content-Type: text/event-stream` is parsed as an
event stream: lines split on `\n` (trailing `\r` stripped); `:` comments
ignored; `event:` sets the type; `data:` lines accumulate joined with `\n`;
a blank line dispatches. `next` decodes an ExecutionResult; `complete` ends
the stream; other event types are ignored. EOF before `complete` is an
error. A `200` that is not `text/event-stream` is decoded as one
ExecutionResult. A non-`200` raises its body's `errors` or the status.

**Cancellation.** The terminal cancels the request's context and closes the
response body; nothing else is sent.

### 6.2 Tests

`tests/test_stream.dang` covers the terminals and `.{{ }}` on plain streams;
`tests/test_subscription.dang` covers each/first/toList/take, break,
continue and return, aliases, a rejected subscription, an event carrying
errors after two good ones, cancellation reaching the server, inline
fragments with `case` dispatch, nested selections, and the `after:`
idiom against a server that completes every connection after two events.
`tests/errors/` pins the two compile errors.
`pkg/gqlsse/client_test.go` covers the request headers, the parsing rules,
the missing `complete`, the JSON fallback, non-200 errors and cancellation.

## 7. Open questions

1. **Racing a stream against other work.** `.first` is the one-value wait;
   how `select`/`timeout`/tasks consume it is for those proposals. Because
   streams are cold, each evaluation of a guard subscribes afresh and losing
   the race cancels it, which is correct with a cursor argument.
2. **Merging.** A subscription operation selects one root field, so N
   independent sources are N connections. The engine merges (one mailbox fed
   by `notify`, `schedule` and `send`); should Dang ever grow `Stream.merge`?
3. **Resumption.** Should a schema convention (`after:` plus a `seq` field)
   let Dang reconnect automatically? Today it is visible code (§3.5).
4. **Partial errors.** graphql-sse allows a `next` with both `data` and
   `errors` and a continuing stream; Dang ends the stream on any error.
5. **Blocking inside agent turns.** Enforced engine-side (a subscription
   issued from within an agent's turn is refused). An advisory Dang-side
   budget is not implemented.
6. **Trace shape.** One span per `.each` iteration, linked to the producer?
