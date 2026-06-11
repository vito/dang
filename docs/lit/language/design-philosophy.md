\use-plugin{dang}

# Design philosophy {#design-philosophy}

> Meta: the "why is it like this" page — keep it opinionated and short. Deliberate omissions live here too: what the language refuses to have is as much philosophy as what it has.

- familiarity over theory
- ergonomics over syntactic purity
- expressiveness over performance
- safety over surprises
- "a leaf in the wind" — low cognitive overhead so brainpower stays on the glued-together product

Just as deliberate is what's *not* in the language:

- no inheritance (only `implements` for interfaces — see [#interfaces-unions])
- no metaprogramming / macros
