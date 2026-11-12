// The WebAssembly face of Dang's tree-sitter grammar: the C tree-sitter
// runtime, treesitter/src/parser.c and scanner.c, and these exports, which
// parse a source and hand Go its tree as a flat array (see ../tswasm.go).
//
// Built into ../dang.wasm by ../build.sh; regenerate with the treesitter-wasm
// Dagger generator after changing anything it compiles.
#include <stdint.h>
#include <stdlib.h>
#include "tree_sitter/api.h"

const TSLanguage *tree_sitter_dang(void);

// node is one tree node, in preorder: its children follow it.
typedef struct {
  uint32_t symbol;
  uint32_t flags;
  uint32_t start_byte;
  uint32_t end_byte;
  uint32_t start_row;
  uint32_t start_column;
  uint32_t end_row;
  uint32_t end_column;
  uint32_t child_count;
} node;

enum {
  FLAG_MISSING = 1 << 0,
  FLAG_ERROR = 1 << 1,
  FLAG_HAS_ERROR = 1 << 2,
  FLAG_EXTRA = 1 << 3,
  FLAG_NAMED = 1 << 4,
};

static TSParser *parser;
static node *nodes;
static uint32_t nodes_len, nodes_cap;
static uint32_t result[2];

__attribute__((export_name("dang_alloc"))) void *dang_alloc(uint32_t size) {
  return malloc(size ? size : 1);
}

__attribute__((export_name("dang_free"))) void dang_free(void *ptr) {
  free(ptr);
}

// dang_symbol_name names a symbol: what tree-sitter calls a node's type.
__attribute__((export_name("dang_symbol_name"))) const char *dang_symbol_name(uint32_t symbol) {
  return ts_language_symbol_name(tree_sitter_dang(), (TSSymbol)symbol);
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

static int walk(TSNode n, int prune) {
  uint32_t i = nodes_len;
  if (!append()) return 0;
  TSPoint start = ts_node_start_point(n), end = ts_node_end_point(n);
  uint32_t flags = 0;
  if (ts_node_is_missing(n)) flags |= FLAG_MISSING;
  if (ts_node_is_error(n)) flags |= FLAG_ERROR;
  if (ts_node_has_error(n)) flags |= FLAG_HAS_ERROR;
  if (ts_node_is_extra(n)) flags |= FLAG_EXTRA;
  if (ts_node_is_named(n)) flags |= FLAG_NAMED;
  uint32_t count = ts_node_child_count(n);
  if (prune && !(flags & FLAG_HAS_ERROR)) count = 0;
  nodes[i] = (node){
    .symbol = ts_node_symbol(n),
    .flags = flags,
    .start_byte = ts_node_start_byte(n),
    .end_byte = ts_node_end_byte(n),
    .start_row = start.row,
    .start_column = start.column,
    .end_row = end.row,
    .end_column = end.column,
    .child_count = count,
  };
  for (uint32_t c = 0; c < count; c++) {
    if (!walk(ts_node_child(n, c), prune)) return 0;
  }
  return 1;
}

// dang_parse parses len bytes at src and returns {node count, nodes}, valid
// until the next call, or NULL if parsing failed. With prune, nodes without
// errors beneath them come back without their children.
__attribute__((export_name("dang_parse"))) uint32_t *dang_parse(const char *src, uint32_t len, uint32_t prune) {
  if (!parser) {
    parser = ts_parser_new();
    if (!ts_parser_set_language(parser, tree_sitter_dang())) return NULL;
  }
  TSTree *tree = ts_parser_parse_string(parser, NULL, src, len);
  if (!tree) return NULL;
  nodes_len = 0;
  int ok = walk(ts_tree_root_node(tree), prune);
  ts_tree_delete(tree);
  if (!ok) return NULL;
  result[0] = nodes_len;
  result[1] = (uint32_t)(uintptr_t)nodes;
  return result;
}
