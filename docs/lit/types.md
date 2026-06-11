\use-plugin{dang}
\split-sections

# Types {#types}

> Meta: the type system is GraphQL's, deliberately — say so up front so schema
> users feel at home. Nullability is the through-line; the other chapters
> build on it.

Dang's type system is GraphQL's: nullability tracked in the types, objects
declared as prototypes, plus interfaces, unions, enums, and scalars that map
1:1 to their schema counterparts.

\table-of-contents

\include-section{./types/nullability.md}
\include-section{./types/flow-typing.md}
\include-section{./types/objects.md}
\include-section{./types/mutation.md}
\include-section{./types/interfaces-unions.md}
\include-section{./types/enums-scalars.md}
