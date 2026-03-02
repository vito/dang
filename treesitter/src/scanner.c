#include "tree_sitter/parser.h"

// External token types — must match the order in the grammar's "externals" array.
enum {
  AUTOMATIC_NEWLINE,
};

void *tree_sitter_dang_external_scanner_create(void) { return NULL; }
void tree_sitter_dang_external_scanner_destroy(void *payload) {}
unsigned tree_sitter_dang_external_scanner_serialize(void *payload, char *buffer) { return 0; }
void tree_sitter_dang_external_scanner_deserialize(void *payload, const char *buffer, unsigned length) {}

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
