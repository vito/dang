\use-plugin{dang}
\split-sections

# The language {#language}

> Meta: the part opener doubles as the mental-model overview — the two leading
> sections (design philosophy, then the distinctives) plant the seeds so the
> chapters have hooks to hang things on. Note: an include-section call after a
> markdown heading nests under that heading, which is why the leading sections
> are include files rather than body headings.

The everyday core: syntax, operators, functions, blocks, control flow, and
errors. If you've written any modern scripting language, most of this part is
"yes, like that" — the differences are flagged where they bite.

\table-of-contents

\include-section{./language/design-philosophy.md}
\include-section{./language/whats-distinctive.md}
\include-section{./language/syntax.md}
\include-section{./language/operators.md}
\include-section{./language/fields-functions.md}
\include-section{./language/blocks.md}
\include-section{./language/control-flow.md}
\include-section{./language/errors.md}
