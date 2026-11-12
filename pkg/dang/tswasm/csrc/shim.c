// The WebAssembly face of Dang's tree-sitter grammar: the C tree-sitter
// runtime, treesitter/src/parser.c and scanner.c, and these exports, which
// parse a source, hand Go its tree as a flat array, and run queries over it
// (see ../tswasm.go).
//
// An instance keeps its last tree, so a query can run over it and the next
// parse can reuse it after an edit (incremental reparsing).
//
// Built into ../dang.wasm by ../build.sh; regenerate with the treesitter-wasm
// Dagger generator after changing anything it compiles.
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "tree_sitter/api.h"

const TSLanguage *tree_sitter_dang(void);

#define EXPORT(name) __attribute__((export_name(#name)))

// dang_interrupted is the host's: it reports whether the call in progress
// should stop (its context is done). Parsing and queries poll it.
__attribute__((import_module("env"), import_name("dang_interrupted")))
extern uint32_t dang_interrupted(void);

// node is one tree node, in preorder: its children follow it.
typedef struct {
  uint32_t symbol;
  uint32_t flags;
  uint32_t field;  // its field in its parent, or 0
  uint32_t start_byte;
  uint32_t end_byte;
  uint32_t start_row;
  uint32_t start_column;
  uint32_t end_row;
  uint32_t end_column;
  uint32_t child_count;  // children that follow; 0 if pruned
} node;

enum {
  FLAG_MISSING = 1 << 0,
  FLAG_ERROR = 1 << 1,
  FLAG_HAS_ERROR = 1 << 2,
  FLAG_EXTRA = 1 << 3,
  FLAG_NAMED = 1 << 4,
  FLAG_PRUNED = 1 << 5,  // has children that were left out
};

// Which subtrees dang_nodes leaves out.
enum {
  PRUNE_NONE = 0,    // none
  PRUNE_ERRORS = 1,  // those without errors beneath them
};

static TSParser *parser;
static TSTree *tree;
static node *nodes;
static uint32_t nodes_len, nodes_cap;
static TSNode *table;  // see dang_root
static uint32_t table_len, table_cap;
static uint32_t result[2];

// dang_result is where calls with more to say than their result say it.
EXPORT(dang_result) uint32_t *dang_result(void) {
  return result;
}

EXPORT(dang_alloc) void *dang_alloc(uint32_t size) {
  return malloc(size ? size : 1);
}

EXPORT(dang_free) void dang_free(void *ptr) {
  free(ptr);
}

EXPORT(dang_symbol_count) uint32_t dang_symbol_count(void) {
  return ts_language_symbol_count(tree_sitter_dang());
}

// dang_symbol_name names a symbol: what tree-sitter calls a node's type.
EXPORT(dang_symbol_name) const char *dang_symbol_name(uint32_t symbol) {
  return ts_language_symbol_name(tree_sitter_dang(), (TSSymbol)symbol);
}

EXPORT(dang_field_count) uint32_t dang_field_count(void) {
  return ts_language_field_count(tree_sitter_dang());
}

// dang_field_name names a field, or returns NULL for 0.
EXPORT(dang_field_name) const char *dang_field_name(uint32_t field) {
  return ts_language_field_name_for_id(tree_sitter_dang(), (TSFieldId)field);
}

static bool parse_progress(TSParseState *state) {
  (void)state;
  return dang_interrupted();
}

static bool query_progress(TSQueryCursorState *state) {
  (void)state;
  return dang_interrupted();
}

// dang_edit records an edit to the source of the last parse, so the next
// dang_parse with reuse can reuse what the edit left alone.
EXPORT(dang_edit) void dang_edit(uint32_t start_byte, uint32_t old_end_byte, uint32_t new_end_byte,
                                 uint32_t start_row, uint32_t start_col,
                                 uint32_t old_end_row, uint32_t old_end_col,
                                 uint32_t new_end_row, uint32_t new_end_col) {
  if (!tree) return;
  TSInputEdit edit = {
    .start_byte = start_byte,
    .old_end_byte = old_end_byte,
    .new_end_byte = new_end_byte,
    .start_point = {start_row, start_col},
    .old_end_point = {old_end_row, old_end_col},
    .new_end_point = {new_end_row, new_end_col},
  };
  ts_tree_edit(tree, &edit);
}

typedef struct {
  const char *src;
  uint32_t len;
} input;

static const char *read_input(void *payload, uint32_t byte, TSPoint point, uint32_t *bytes_read) {
  (void)point;
  input *in = payload;
  if (byte >= in->len) {
    *bytes_read = 0;
    return "";
  }
  *bytes_read = in->len - byte;
  return in->src + byte;
}

// dang_parse parses len bytes at src, keeping the tree for dang_nodes and
// dang_captures_exec. With reuse it reuses the last tree, which dang_edit
// must have told of every change since. It returns 0 if parsing failed or
// was interrupted, and drops the last tree either way.
EXPORT(dang_parse) uint32_t dang_parse(const char *src, uint32_t len, uint32_t reuse) {
  if (!parser) {
    parser = ts_parser_new();
    if (!ts_parser_set_language(parser, tree_sitter_dang())) return 0;
  }
  input in = {src, len};
  TSInput ts_input = {
    .payload = &in,
    .read = read_input,
    .encoding = TSInputEncodingUTF8,
  };
  TSParseOptions options = {.payload = NULL, .progress_callback = parse_progress};
  TSTree *parsed = ts_parser_parse_with_options(parser, reuse ? tree : NULL, ts_input, options);
  if (tree) ts_tree_delete(tree);
  tree = parsed;
  table_len = 0;  // the nodes read of the last tree are gone with it
  if (!tree) {
    ts_parser_reset(parser);
    return 0;
  }
  return 1;
}

static int append(void) {
  if (nodes_len == nodes_cap) {
    uint32_t cap = nodes_cap ? nodes_cap * 2 : 256;
    node *grown = realloc(nodes, cap * sizeof(node));
    if (!grown) return 0;
    nodes = grown;
    nodes_cap = cap;
  }
  nodes_len++;
  return 1;
}

static uint32_t node_flags(TSNode n) {
  uint32_t flags = 0;
  if (ts_node_is_missing(n)) flags |= FLAG_MISSING;
  if (ts_node_is_error(n)) flags |= FLAG_ERROR;
  if (ts_node_has_error(n)) flags |= FLAG_HAS_ERROR;
  if (ts_node_is_extra(n)) flags |= FLAG_EXTRA;
  if (ts_node_is_named(n)) flags |= FLAG_NAMED;
  return flags;
}

static void fill(node *out, TSNode n, uint32_t flags, uint32_t field, uint32_t child_count) {
  TSPoint start = ts_node_start_point(n), end = ts_node_end_point(n);
  *out = (node){
    .symbol = ts_node_symbol(n),
    .flags = flags,
    .field = field,
    .start_byte = ts_node_start_byte(n),
    .end_byte = ts_node_end_byte(n),
    .start_row = start.row,
    .start_column = start.column,
    .end_row = end.row,
    .end_column = end.column,
    .child_count = child_count,
  };
}

// walk records the cursor's node and, unless pruned, its subtree.
static int walk(TSTreeCursor *cursor, uint32_t prune) {
  TSNode n = ts_tree_cursor_current_node(cursor);
  uint32_t i = nodes_len;
  if (!append()) return 0;
  uint32_t flags = node_flags(n);
  bool expand = prune != PRUNE_ERRORS || (flags & FLAG_HAS_ERROR);
  uint32_t count = 0;
  if (ts_tree_cursor_goto_first_child(cursor)) {
    if (expand) {
      do {
        if (!walk(cursor, prune)) return 0;
        count++;
      } while (ts_tree_cursor_goto_next_sibling(cursor));
    } else {
      flags |= FLAG_PRUNED;
    }
    ts_tree_cursor_goto_parent(cursor);
  }
  fill(&nodes[i], n, flags, ts_tree_cursor_current_field_id(cursor), count);
  return 1;
}

// dang_nodes returns the kept tree as {node count, nodes}, valid until the
// next call, or NULL without one. prune is one of the PRUNE_ modes.
EXPORT(dang_nodes) uint32_t *dang_nodes(uint32_t prune) {
  if (!tree) return NULL;
  nodes_len = 0;
  TSTreeCursor cursor = ts_tree_cursor_new(ts_tree_root_node(tree));
  int ok = walk(&cursor, prune);
  ts_tree_cursor_delete(&cursor);
  if (!ok) return NULL;
  result[0] = nodes_len;
  result[1] = (uint32_t)(uintptr_t)nodes;
  return result;
}

// Reading the kept tree a node's children at a time, for callers that look
// at only part of it. The nodes read so far are kept in a table, by index;
// a node's child_count is how many children dang_children will return.
static TSTreeCursor lazy_cursor;
static bool lazy_cursor_live;

static int table_push(TSNode n) {
  if (table_len == table_cap) {
    uint32_t cap = table_cap ? table_cap * 2 : 256;
    TSNode *grown = realloc(table, cap * sizeof(TSNode));
    if (!grown) return 0;
    table = grown;
    table_cap = cap;
  }
  table[table_len++] = n;
  return 1;
}

// dang_root starts reading the kept tree afresh, and returns its root, of
// index 0, or NULL without a tree.
EXPORT(dang_root) node *dang_root(void) {
  if (!tree) return NULL;
  TSNode root = ts_tree_root_node(tree);
  table_len = 0;
  nodes_len = 0;
  if (!table_push(root) || !append()) return NULL;
  fill(&nodes[0], root, node_flags(root), 0, ts_node_child_count(root));
  return nodes;
}

// dang_children returns the children of the node of index i, valid until
// the next call; result is {how many, the index of the first}, and the rest
// follow it.
EXPORT(dang_children) node *dang_children(uint32_t i) {
  if (i >= table_len) return NULL;
  TSNode parent = table[i];
  if (lazy_cursor_live) {
    ts_tree_cursor_reset(&lazy_cursor, parent);
  } else {
    lazy_cursor = ts_tree_cursor_new(parent);
    lazy_cursor_live = true;
  }
  nodes_len = 0;
  result[1] = table_len;
  if (ts_tree_cursor_goto_first_child(&lazy_cursor)) {
    do {
      TSNode n = ts_tree_cursor_current_node(&lazy_cursor);
      if (!table_push(n) || !append()) return NULL;
      fill(&nodes[nodes_len - 1], n, node_flags(n), ts_tree_cursor_current_field_id(&lazy_cursor),
           ts_node_child_count(n));
    } while (ts_tree_cursor_goto_next_sibling(&lazy_cursor));
  }
  result[0] = nodes_len;
  return nodes;
}

// Queries.

// dang_query_new compiles a query, or returns NULL and sets result to
// {error offset, error type}.
EXPORT(dang_query_new) TSQuery *dang_query_new(const char *src, uint32_t len) {
  uint32_t offset = 0;
  TSQueryError err = TSQueryErrorNone;
  TSQuery *q = ts_query_new(tree_sitter_dang(), src, len, &offset, &err);
  result[0] = offset;
  result[1] = err;
  return q;
}

EXPORT(dang_query_delete) void dang_query_delete(TSQuery *q) {
  ts_query_delete(q);
}

EXPORT(dang_query_capture_count) uint32_t dang_query_capture_count(const TSQuery *q) {
  return ts_query_capture_count(q);
}

EXPORT(dang_query_pattern_count) uint32_t dang_query_pattern_count(const TSQuery *q) {
  return ts_query_pattern_count(q);
}

// dang_query_capture_name returns a capture's name; result[0] is its length.
EXPORT(dang_query_capture_name) const char *dang_query_capture_name(const TSQuery *q, uint32_t id) {
  return ts_query_capture_name_for_id(q, id, &result[0]);
}

// dang_query_string_value returns a predicate string; result[0] is its length.
EXPORT(dang_query_string_value) const char *dang_query_string_value(const TSQuery *q, uint32_t id) {
  return ts_query_string_value_for_id(q, id, &result[0]);
}

// dang_query_predicates returns a pattern's predicate steps ({type, value
// id} each); result[0] is how many.
EXPORT(dang_query_predicates) const TSQueryPredicateStep *dang_query_predicates(const TSQuery *q, uint32_t pattern) {
  return ts_query_predicates_for_pattern(q, pattern, &result[0]);
}

static TSQueryCursor *qcursor;
static uint32_t *event;
static uint32_t event_cap;

// dang_captures_exec runs a query over the kept tree; dang_captures_next
// then steps through its captures in order. It returns 0 without a tree.
EXPORT(dang_captures_exec) uint32_t dang_captures_exec(const TSQuery *q) {
  // The cursor keeps a pointer to its options, so they must outlive this.
  static TSQueryCursorOptions options = {.payload = NULL, .progress_callback = query_progress};
  if (!tree) return 0;
  if (!qcursor) qcursor = ts_query_cursor_new();
  ts_query_cursor_exec_with_options(qcursor, q, ts_tree_root_node(tree), &options);
  return 1;
}

// dang_captures_next returns the next capture as {match id, pattern index,
// capture index within the match, capture count, then each of the match's
// captures as {capture id, start byte, end byte}}, valid until the next call,
// or NULL when there are no more (or the query was interrupted).
EXPORT(dang_captures_next) uint32_t *dang_captures_next(void) {
  TSQueryMatch m;
  uint32_t idx;
  if (!ts_query_cursor_next_capture(qcursor, &m, &idx)) return NULL;
  uint32_t need = 4 + 3 * (uint32_t)m.capture_count;
  if (need > event_cap) {
    uint32_t *grown = realloc(event, need * sizeof(uint32_t));
    if (!grown) return NULL;
    event = grown;
    event_cap = need;
  }
  event[0] = m.id;
  event[1] = m.pattern_index;
  event[2] = idx;
  event[3] = m.capture_count;
  for (uint16_t c = 0; c < m.capture_count; c++) {
    event[4 + 3 * c] = m.captures[c].index;
    event[5 + 3 * c] = ts_node_start_byte(m.captures[c].node);
    event[6 + 3 * c] = ts_node_end_byte(m.captures[c].node);
  }
  return event;
}

// dang_captures_remove drops a match whose predicates failed, so none of its
// later captures come up.
EXPORT(dang_captures_remove) void dang_captures_remove(uint32_t match_id) {
  ts_query_cursor_remove_match(qcursor, match_id);
}
