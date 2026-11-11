#include "tree_sitter/parser.h"
#include <stdlib.h>
#include <string.h>

// External token types — must match the order in the grammar's "externals" array.
enum {
  AUTOMATIC_NEWLINE,
  INLINE_SPACE,
  TEMPLATE_MULTI_OPEN,
  TEMPLATE_MULTI_CLOSE,
  TEMPLATE_CONTENT_CHAR,
  LANG_TAG_TERMINATOR,
  COMMENT_TOKEN,
  LANG_TAG_NAME,
  STRING_CONTENT,
  TRIPLE_QUOTE_CONTENT,
  SINGLE_QUOTE_CONTENT,
  TRIPLE_SINGLE_QUOTE_CONTENT,
};

// Maximum nesting depth for backtick templates. Each level stores the open
// fence length so the matching close fence can be enforced.
#define TEMPLATE_MAX_DEPTH 32

typedef struct {
  unsigned char depth;
  unsigned char fence_stack[TEMPLATE_MAX_DEPTH];
} Scanner;

void *tree_sitter_dang_external_scanner_create(void) {
  Scanner *s = (Scanner *)calloc(1, sizeof(Scanner));
  return s;
}

void tree_sitter_dang_external_scanner_destroy(void *payload) {
  free(payload);
}

unsigned tree_sitter_dang_external_scanner_serialize(void *payload, char *buffer) {
  Scanner *s = (Scanner *)payload;
  unsigned n = 0;
  buffer[n++] = (char)s->depth;
  for (unsigned i = 0; i < s->depth && i < TEMPLATE_MAX_DEPTH; i++) {
    buffer[n++] = (char)s->fence_stack[i];
  }
  return n;
}

void tree_sitter_dang_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {
  Scanner *s = (Scanner *)payload;
  s->depth = 0;
  memset(s->fence_stack, 0, sizeof(s->fence_stack));
  if (length == 0) {
    return;
  }
  unsigned d = (unsigned char)buffer[0];
  if (d > TEMPLATE_MAX_DEPTH) {
    d = TEMPLATE_MAX_DEPTH;
  }
  s->depth = (unsigned char)d;
  for (unsigned i = 0; i < d && (i + 1) < length; i++) {
    s->fence_stack[i] = (unsigned char)buffer[i + 1];
  }
}

// Returns true if `c` is a word character (matches [a-zA-Z0-9_]).
static bool is_word_char(int32_t c) {
  return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
         (c >= '0' && c <= '9') || c == '_';
}

static bool is_alpha(int32_t c) {
  return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z');
}

static bool is_hex_digit(int32_t c) {
  return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') ||
         (c >= 'A' && c <= 'F');
}

// Checks if the upcoming characters form the keyword `and` or `or` followed by
// a non-word character (space, newline, paren, etc.). This is used to treat
// leading `and`/`or` on a new line as expression continuation.
//
// The lexer is advanced past the keyword, so the caller must mark_end the
// token before calling this; otherwise a false return emits a token that has
// swallowed the advanced characters (e.g. the `a` of `agent`).
static bool is_leading_logical_op(TSLexer *lexer) {
  // Check for "and"
  if (lexer->lookahead == 'a') {
    lexer->advance(lexer, false);
    if (lexer->lookahead == 'n') {
      lexer->advance(lexer, false);
      if (lexer->lookahead == 'd') {
        lexer->advance(lexer, false);
        if (!is_word_char(lexer->lookahead)) {
          return true;
        }
      }
    }
    return false;
  }
  // Check for "or"
  if (lexer->lookahead == 'o') {
    lexer->advance(lexer, false);
    if (lexer->lookahead == 'r') {
      lexer->advance(lexer, false);
      if (!is_word_char(lexer->lookahead)) {
        return true;
      }
    }
    return false;
  }
  return false;
}


// Scan a multi-line backtick template opening fence: 3 or more backticks
// not followed by another backtick. Records the fence length on the stack.
//
// `at_newline` tells whether the caller has already determined it's safe to
// also skip leading newlines (in addition to spaces/tabs) — used so a fence
// can open on a fresh line inside paren-arg contexts. Callers must only set
// it when AUTOMATIC_NEWLINE isn't otherwise valid at this position: peeking
// past a newline advances the shared lexer position for the rest of this
// scanner dispatch, which would otherwise prevent the subsequent
// automatic_newline check from emitting a statement separator (and derail
// error recovery for things like unterminated strings).
static bool scan_template_open(Scanner *s, TSLexer *lexer, bool at_newline) {
  if (s->depth >= TEMPLATE_MAX_DEPTH) {
    return false;
  }
  // The external scanner runs before extras are skipped, so consume any
  // leading inline whitespace ourselves (as extras, via skip=true).
  while (lexer->lookahead == ' ' || lexer->lookahead == '\t' ||
         (at_newline && (lexer->lookahead == '\n' || lexer->lookahead == '\r'))) {
    lexer->advance(lexer, true);
  }
  if (lexer->lookahead != '`') {
    return false;
  }
  unsigned count = 0;
  while (lexer->lookahead == '`') {
    count++;
    lexer->advance(lexer, false);
  }
  if (count < 3) {
    return false;
  }
  s->fence_stack[s->depth++] = (unsigned char)(count > 255 ? 255 : count);
  lexer->result_symbol = TEMPLATE_MULTI_OPEN;
  return true;
}

// Scan a closing fence: the first N backticks close the current template,
// where N matches the current top of the fence stack. Any extra backticks are
// left for parser recovery, making longer runs invalid instead of content.
static bool scan_template_close(Scanner *s, TSLexer *lexer) {
  if (s->depth == 0 || lexer->lookahead != '`') {
    return false;
  }
  unsigned expected = s->fence_stack[s->depth - 1];
  for (unsigned i = 0; i < expected; i++) {
    if (lexer->lookahead != '`') {
      return false;
    }
    lexer->advance(lexer, false);
  }
  s->depth--;
  lexer->result_symbol = TEMPLATE_MULTI_CLOSE;
  return true;
}

// Scan the newline that terminates a language tag. Only valid right after a
// lang_tag_name, which made sure the newline is there.
static bool scan_lang_tag_terminator(TSLexer *lexer) {
  if (lexer->lookahead != '\n') {
    return false;
  }
  lexer->advance(lexer, false);
  lexer->result_symbol = LANG_TAG_TERMINATOR;
  return true;
}

// Scan one piece of template content: one or more bytes that are neither a
// ${...} interpolation marker nor part of a matching close fence. Shorter
// backtick runs are content; longer runs are invalid and left for parser
// recovery.
//
// The grammar marks TEMPLATE_CONTENT_CHAR valid inside both single-line
// (single backtick) and multi-line (triple+ backtick) templates. In the
// multi case, depth > 0 and the fence stack tracks the matching close
// length. In the single case, depth is 0 and any `\`` is the close.
static bool scan_template_content_char(Scanner *s, TSLexer *lexer) {
  if (lexer->eof(lexer)) {
    return false;
  }
  // In single-line templates, a newline is a parse error (no closing
  // backtick on this line). Refuse so the parser can recover.
  if (s->depth == 0 && lexer->lookahead == '\n') {
    return false;
  }
  if (s->depth > 0) {
    if (lexer->lookahead == '`') {
      unsigned expected = s->fence_stack[s->depth - 1];
      unsigned count = 0;
      while (lexer->lookahead == '`') {
        count++;
        lexer->advance(lexer, false);
      }
      if (count >= expected) {
        // Matching close fence or invalid longer run; not content.
        return false;
      }
      // Shorter backtick runs are content; we've already advanced past them.
      lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
      return true;
    }
  } else if (lexer->lookahead == '`') {
    // Single-line template: any backtick closes; let the parser take it.
    return false;
  }
  if (lexer->lookahead == '\\') {
    lexer->advance(lexer, false);
    if (lexer->lookahead == '$') {
      lexer->advance(lexer, false);
      if (lexer->lookahead == '{') {
        lexer->advance(lexer, false);
      }
    }
    lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
    return true;
  }
  if (lexer->lookahead == '$') {
    lexer->advance(lexer, false);
    if (lexer->lookahead == '{') {
      return false;
    }
    lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
    return true;
  }
  lexer->advance(lexer, false);
  lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
  return true;
}

// Advance past the rest of a backslash escape (the backslash is already
// consumed), reporting whether it's one the grammar's immediate_escape
// accepts: \a \b \f \n \r \t \v \" \\ \/ \$, \xNN, \uNNNN, \UNNNNNNNN or \NNN
// (octal). Only characters that can continue the escape are consumed, so none
// of them ends string content.
static bool scan_escape_rest(TSLexer *lexer) {
  int32_t c = lexer->lookahead;
  switch (c) {
    case 'a': case 'b': case 'f': case 'n': case 'r': case 't': case 'v':
    case '"': case '\\': case '/': case '$':
      lexer->advance(lexer, false);
      return true;
  }
  unsigned digits = 0;
  if (c == 'x') {
    digits = 2;
  } else if (c == 'u') {
    digits = 4;
  } else if (c == 'U') {
    digits = 8;
  }
  if (digits > 0) {
    lexer->advance(lexer, false);
    for (unsigned i = 0; i < digits; i++) {
      if (!is_hex_digit(lexer->lookahead)) {
        return false;
      }
      lexer->advance(lexer, false);
    }
    return true;
  }
  for (unsigned i = 0; i < 3; i++) {
    if (lexer->lookahead < '0' || lexer->lookahead > '7') {
      return false;
    }
    lexer->advance(lexer, false);
  }
  return true;
}

// Scan the text of a `"..."` string up to an escape, an interpolation, the
// closing quote or the end of the line. `has_content` tells whether the
// caller already consumed (and marked) content.
static bool scan_string_content(TSLexer *lexer, bool has_content) {
  while (!lexer->eof(lexer)) {
    int32_t c = lexer->lookahead;
    if (c == '"' || c == '\\' || c == '\n') {
      break;
    }
    lexer->advance(lexer, false);
    if (c == '$' && lexer->lookahead == '{') {
      break;
    }
    has_content = true;
    lexer->mark_end(lexer);
  }
  if (!has_content) {
    return false;
  }
  lexer->result_symbol = STRING_CONTENT;
  return true;
}

// Scan the text of a triple-quoted string (`"""` or `'''`) up to its closing
// quotes. One or two quotes are content; three end it. In a `"""` string
// (cooked), escapes and interpolations end the content too. A backslash
// starting no escape is taken as content rather than left as an error,
// because docstrings share this token (see patchDocStringShape) and are
// verbatim.
static bool scan_triple_content(TSLexer *lexer, int32_t quote, bool cooked, bool has_content) {
  while (!lexer->eof(lexer)) {
    int32_t c = lexer->lookahead;
    if (c == quote) {
      unsigned count = 0;
      while (lexer->lookahead == quote && count < 3) {
        lexer->advance(lexer, false);
        count++;
      }
      if (count == 3) {
        break;
      }
    } else if (cooked && c == '\\') {
      lexer->advance(lexer, false);
      if (scan_escape_rest(lexer)) {
        break;
      }
    } else if (cooked && c == '$') {
      lexer->advance(lexer, false);
      if (lexer->lookahead == '{') {
        break;
      }
    } else {
      lexer->advance(lexer, false);
    }
    has_content = true;
    lexer->mark_end(lexer);
  }
  if (!has_content) {
    return false;
  }
  lexer->result_symbol = cooked ? TRIPLE_QUOTE_CONTENT : TRIPLE_SINGLE_QUOTE_CONTENT;
  return true;
}

// Scan the text of a `'...'` string: anything up to the closing quote or the
// end of the line.
static bool scan_single_quote_content(TSLexer *lexer) {
  bool has_content = false;
  while (!lexer->eof(lexer) && lexer->lookahead != '\'' &&
         lexer->lookahead != '\n') {
    lexer->advance(lexer, false);
    has_content = true;
  }
  if (!has_content) {
    return false;
  }
  lexer->result_symbol = SINGLE_QUOTE_CONTENT;
  return true;
}

// Scan the language tag of a multi-line string, right after its opening
// fence: a word ([A-Za-z][A-Za-z0-9_-]*) alone on the opening line. When the
// line holds anything else, the word is content of the string instead: a
// template content char of its first letter, or the start of the
// triple-quoted string's content.
static bool scan_lang_tag_name(TSLexer *lexer, const bool *valid_symbols) {
  lexer->advance(lexer, false);
  lexer->mark_end(lexer);
  while (is_alpha(lexer->lookahead) ||
         (lexer->lookahead >= '0' && lexer->lookahead <= '9') ||
         lexer->lookahead == '_' || lexer->lookahead == '-') {
    lexer->advance(lexer, false);
  }
  if (lexer->lookahead == '\n') {
    lexer->mark_end(lexer);
    lexer->result_symbol = LANG_TAG_NAME;
    return true;
  }
  if (valid_symbols[TEMPLATE_CONTENT_CHAR]) {
    // The token ends after the first letter, as template content chars do.
    lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
    return true;
  }
  // The whole word is content; carry on to the rest of it.
  lexer->mark_end(lexer);
  if (valid_symbols[TRIPLE_QUOTE_CONTENT]) {
    return scan_triple_content(lexer, '"', true, true);
  }
  if (valid_symbols[TRIPLE_SINGLE_QUOTE_CONTENT]) {
    return scan_triple_content(lexer, '\'', false, true);
  }
  return false;
}

// Whether the parser is inside string or template content, where `#` is text
// rather than the start of a comment.
static bool in_string_content(const bool *valid_symbols) {
  return valid_symbols[TEMPLATE_CONTENT_CHAR] ||
         valid_symbols[STRING_CONTENT] ||
         valid_symbols[TRIPLE_QUOTE_CONTENT] ||
         valid_symbols[SINGLE_QUOTE_CONTENT] ||
         valid_symbols[TRIPLE_SINGLE_QUOTE_CONTENT];
}

// Scan a `# ...` comment. Refuses whenever the parser would also accept
// string or template content, so `#` in a string (Markdown headers,
// `issue #5`, etc.) is treated as string content rather than absorbed into a
// comment by the extras lexer.
static bool scan_comment_token(TSLexer *lexer, const bool *valid_symbols, bool recovering) {
  if (!recovering && in_string_content(valid_symbols)) {
    return false;
  }
  if (lexer->lookahead != '#') {
    return false;
  }
  lexer->advance(lexer, false);
  while (!lexer->eof(lexer) && lexer->lookahead != '\n') {
    lexer->advance(lexer, false);
  }
  lexer->result_symbol = COMMENT_TOKEN;
  return true;
}

// Scan for an automatic newline separator.
//
// A newline acts as a statement separator UNLESS the first
// non-whitespace character on a subsequent line is a continuation
// token (dot, opening brace, pipe). Blank lines are skipped when
// making this determination.
//
// When the newline should NOT be a separator, the scanner returns
// false so tree-sitter treats it as whitespace (via extras), allowing
// multi-line expressions to be parsed as a single chain.
bool tree_sitter_dang_external_scanner_scan(
  void *payload,
  TSLexer *lexer,
  const bool *valid_symbols
) {
  Scanner *s = (Scanner *)payload;

  // During error recovery every external token is valid at once; content
  // of two kinds of string never is otherwise. Content tokens would swallow
  // whatever follows (into hidden tokens no state can take, so the parser
  // would never resynchronize), so leave recovery to the other tokens.
  bool recovering = valid_symbols[STRING_CONTENT] && valid_symbols[SINGLE_QUOTE_CONTENT];

  // Template and string tokens are checked before the generic newline/space
  // ones because their content scanners may consume whitespace inside
  // strings.
  if (!recovering && valid_symbols[LANG_TAG_TERMINATOR] && scan_lang_tag_terminator(lexer)) {
    return true;
  }
  if (valid_symbols[TEMPLATE_MULTI_CLOSE] && scan_template_close(s, lexer)) {
    return true;
  }
  // Skip leading newlines only when AUTOMATIC_NEWLINE isn't otherwise valid
  // here — otherwise the newline-skip would steal a statement separator. In
  // the contexts where the newline-skip matters (fences opening on a fresh
  // line after `(` or `,` in arg lists), AUTOMATIC_NEWLINE isn't valid yet.
  if (valid_symbols[TEMPLATE_MULTI_OPEN] &&
      scan_template_open(s, lexer, !valid_symbols[AUTOMATIC_NEWLINE])) {
    return true;
  }
  // A newline in a single-line string means it's unterminated. Tree-sitter's
  // lexer, sharing a lex state with tokens that may follow whitespace, would
  // otherwise skip the newline and read on into the next line; offering the
  // newline as a statement separator instead (invalid here) lets error
  // recovery close the string with a missing quote and carry on.
  bool unterminated = false;
  if (!recovering) {
    if (valid_symbols[LANG_TAG_NAME] && is_alpha(lexer->lookahead)) {
      return scan_lang_tag_name(lexer, valid_symbols);
    }
    if (valid_symbols[STRING_CONTENT]) {
      if (scan_string_content(lexer, false)) {
        return true;
      }
      unterminated = lexer->lookahead == '\n';
    } else if (valid_symbols[TRIPLE_QUOTE_CONTENT]) {
      return scan_triple_content(lexer, '"', true, false);
    } else if (valid_symbols[SINGLE_QUOTE_CONTENT]) {
      if (scan_single_quote_content(lexer)) {
        return true;
      }
      unterminated = lexer->lookahead == '\n';
    } else if (valid_symbols[TRIPLE_SINGLE_QUOTE_CONTENT]) {
      return scan_triple_content(lexer, '\'', false, false);
    }
    if (!unterminated && in_string_content(valid_symbols) &&
        !valid_symbols[TEMPLATE_CONTENT_CHAR]) {
      return false;
    }
  }
  if (!recovering && valid_symbols[TEMPLATE_CONTENT_CHAR] && scan_template_content_char(s, lexer)) {
    return true;
  }

  // Trailing comments and `_inlineSpace` both begin with a run of spaces or
  // tabs. The internal lexer only skips extras before *internal* tokens, so
  // unless the run is consumed here, a comment after code on the same line
  // (`foo { 1 } # note`) never reaches the comment scanner. Skip the run as
  // whitespace once, then decide by what follows.
  bool saw_inline_space = false;
  while (lexer->lookahead == ' ' || lexer->lookahead == '\t') {
    lexer->advance(lexer, true);
    saw_inline_space = true;
  }

  if (valid_symbols[COMMENT_TOKEN] && scan_comment_token(lexer, valid_symbols, recovering)) {
    return true;
  }

  // `_inlineSpace` is used before required same-line values. Do not let
  // comments or newlines be consumed as extras before the value. The spaces
  // were skipped above, so the token is zero-width at the value's position.
  // A closer or comma can never start a value either — refusing those lets
  // the parser take the no-value branch of `break`/`return` (the PEG
  // backtracks out of `_inlineSpace Form`, but tree-sitter commits once the
  // token is emitted, turning `for { break }` into an error).
  if (valid_symbols[INLINE_SPACE] && saw_inline_space &&
      lexer->lookahead != '\n' && lexer->lookahead != '\r' &&
      lexer->lookahead != '#' && lexer->lookahead != '}' &&
      lexer->lookahead != ')' && lexer->lookahead != ']' &&
      lexer->lookahead != ',' && !lexer->eof(lexer)) {
    lexer->result_symbol = INLINE_SPACE;
    return true;
  }

  if (!valid_symbols[AUTOMATIC_NEWLINE] && !unterminated) {
    return false;
  }

  // We must be looking at a newline character.
  if (lexer->lookahead != '\n') {
    return false;
  }

  // Consume the newline.
  lexer->advance(lexer, false);

  // Skip ALL whitespace including blank lines to find the first
  // significant character on a subsequent line.
  while (lexer->lookahead == ' ' || lexer->lookahead == '\t' ||
         lexer->lookahead == '\r' || lexer->lookahead == '\n') {
    lexer->advance(lexer, false);
  }

  // The separator token ends here; everything past this point is pure
  // lookahead and must not be included in the token.
  lexer->mark_end(lexer);

  // A leading `.` continues a method chain — unless it's a `...` spread
  // starting another inline fragment in a selection, where the newline IS
  // the separator between entries. We're past mark_end, so this is pure
  // lookahead.
  if (lexer->lookahead == '.') {
    lexer->advance(lexer, false);
    if (lexer->lookahead != '.') {
      return false;
    }
    lexer->result_symbol = AUTOMATIC_NEWLINE;
    return true;
  }

  // `|` at the start of the next line continues the previous expression (pipe
  // operator), so the newline is not a separator.
  if (lexer->lookahead == '|') {
    return false;
  }

  // `{` at the start of the next line usually continues the previous
  // expression as a block-argument body — the PEG's `SymbolBlock <- Symbol _
  // BlockArg` allows a newline before the block. But `{{` opens an object
  // literal, which is a *new* statement: block-arg bodies open with a single
  // `{`, and a bare `{{ … }}` is never a selection (those need a leading `.`,
  // handled above). Without telling them apart, `foo` ⏎ `{{ … }}` mis-parses
  // as `foo { … }` and errors on the record fields, where the PEG instead
  // backtracks out of the block reading. Peek the second brace to decide.
  // (We're past mark_end, so advancing is pure lookahead and does not extend
  // the separator token.)
  if (lexer->lookahead == '{') {
    lexer->advance(lexer, false);
    if (lexer->lookahead != '{') {
      return false;  // single `{`: block-arg body continuation
    }
    // `{{`: a bare object literal begins a new statement.
    lexer->result_symbol = AUTOMATIC_NEWLINE;
    return true;
  }

  // A newline before a closing delimiter is plain whitespace: no grammar
  // rule requires a separator there, and `block_arg`/`object_selection`/
  // `arg_values` place separators *between* elements, so emitting one would
  // wrongly demand another element (the PEG backtracks out of this; the
  // tree-sitter lexer cannot).
  if (lexer->lookahead == '}' || lexer->lookahead == ')' ||
      lexer->lookahead == ']') {
    return false;
  }

  // Check for leading `and`/`or` keyword (logical operator continuation).
  if (is_leading_logical_op(lexer)) {
    return false;
  }

  // If we hit EOF, don't emit a separator.
  if (lexer->eof(lexer)) {
    return false;
  }

  // Otherwise, this newline IS a statement separator.
  lexer->result_symbol = AUTOMATIC_NEWLINE;
  return true;
}
