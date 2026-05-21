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

// Returns true if `c` is a character that, when it appears as the first
// non-whitespace character on a subsequent line, indicates continuation
// of the previous expression (i.e. the newline is NOT a statement separator).
static bool is_continuation_start(int32_t c) {
  switch (c) {
    case '.':  // method chain
    case '{':  // block arg or object selection
    case '|':  // pipe operator
      return true;
    default:
      return false;
  }
}

// Checks if the upcoming characters form the keyword `and` or `or` followed by
// a non-word character (space, newline, paren, etc.). This is used to treat
// leading `and`/`or` on a new line as expression continuation.
//
// The lexer is advanced past the keyword; the caller should only call this when
// a false return means the scan will return true (AUTOMATIC_NEWLINE), so
// advancing is harmless since tree-sitter will re-lex from the marked position.
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

static bool scan_inline_space(TSLexer *lexer) {
  if (lexer->lookahead != ' ' && lexer->lookahead != '\t') {
    return false;
  }

  do {
    lexer->advance(lexer, false);
  } while (lexer->lookahead == ' ' || lexer->lookahead == '\t');

  // `_inlineSpace` is used before required same-line values. Do not let
  // comments or newlines be consumed as extras before the value.
  if (lexer->lookahead == '\n' || lexer->lookahead == '\r' ||
      lexer->lookahead == '#' || lexer->eof(lexer)) {
    return false;
  }

  lexer->result_symbol = INLINE_SPACE;
  return true;
}

// Scan a multi-line backtick template opening fence: 3 or more backticks
// not followed by another backtick. Records the fence length on the stack.
static bool scan_template_open(Scanner *s, TSLexer *lexer) {
  if (s->depth >= TEMPLATE_MAX_DEPTH) {
    return false;
  }
  // The external scanner runs before extras are skipped, so consume any
  // leading inline whitespace ourselves (as extras, via skip=true) so we
  // see the actual fence start.
  while (lexer->lookahead == ' ' || lexer->lookahead == '\t') {
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

// Scan a closing fence: exactly N consecutive backticks where N matches the
// current top of the fence stack. Pops the stack on success.
static bool scan_template_close(Scanner *s, TSLexer *lexer) {
  if (s->depth == 0 || lexer->lookahead != '`') {
    return false;
  }
  unsigned expected = s->fence_stack[s->depth - 1];
  unsigned count = 0;
  while (lexer->lookahead == '`') {
    count++;
    lexer->advance(lexer, false);
  }
  if (count != expected) {
    return false;
  }
  s->depth--;
  lexer->result_symbol = TEMPLATE_MULTI_CLOSE;
  return true;
}

// Scan one piece of template content: one or more bytes that are neither a
// dollar (so the parser can match $$ / ${...}) nor part of a matching close
// fence. A run of backticks whose length doesn't match the open fence is
// consumed as content.
static bool scan_template_content_char(Scanner *s, TSLexer *lexer) {
  if (s->depth == 0 || lexer->eof(lexer)) {
    return false;
  }
  if (lexer->lookahead == '$') {
    return false;
  }
  if (lexer->lookahead == '`') {
    unsigned expected = s->fence_stack[s->depth - 1];
    unsigned count = 0;
    while (lexer->lookahead == '`') {
      count++;
      lexer->advance(lexer, false);
    }
    if (count == expected) {
      // This is the matching close fence; do not consume.
      return false;
    }
    // Otherwise the backtick run is content; we've already advanced past it.
    lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
    return true;
  }
  lexer->advance(lexer, false);
  lexer->result_symbol = TEMPLATE_CONTENT_CHAR;
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

  // Template tokens are checked before the generic newline/space ones because
  // their content scanner may consume whitespace inside templates.
  if (valid_symbols[TEMPLATE_MULTI_CLOSE] && scan_template_close(s, lexer)) {
    return true;
  }
  if (valid_symbols[TEMPLATE_MULTI_OPEN] && scan_template_open(s, lexer)) {
    return true;
  }
  if (valid_symbols[TEMPLATE_CONTENT_CHAR] && scan_template_content_char(s, lexer)) {
    return true;
  }

  if (valid_symbols[INLINE_SPACE] && scan_inline_space(lexer)) {
    return true;
  }

  if (!valid_symbols[AUTOMATIC_NEWLINE]) {
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

  // If the first significant character indicates expression continuation,
  // this newline is NOT a separator.
  if (is_continuation_start(lexer->lookahead)) {
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
