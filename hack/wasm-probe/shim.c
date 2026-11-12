// A minimal C entry point into tree-sitter for the wasm probe: parse a Dang
// source and report its first ERROR or MISSING node.
#include <stdint.h>
#include <stdlib.h>
#include "tree_sitter/api.h"

const TSLanguage *tree_sitter_dang(void);

static TSParser *parser;
static uint32_t out[7];

__attribute__((export_name("dang_alloc"))) void *dang_alloc(uint32_t n) { return malloc(n); }
__attribute__((export_name("dang_free"))) void dang_free(void *p) { free(p); }

static int find(TSNode n) {
  if (ts_node_is_missing(n) || ts_node_is_error(n)) {
    TSPoint s = ts_node_start_point(n), e = ts_node_end_point(n);
    out[0] = ts_node_is_missing(n);
    out[1] = s.row;
    out[2] = s.column;
    out[3] = e.row;
    out[4] = e.column;
    out[5] = ts_node_symbol(n);
    out[6] = ts_node_child_count(n);
    return 1;
  }
  uint32_t c = ts_node_child_count(n);
  for (uint32_t i = 0; i < c; i++) {
    if (find(ts_node_child(n, i))) return 1;
  }
  return 0;
}

// dang_first_error returns a pointer to {missing, start row, start col, end
// row, end col, symbol, child count} for the first error, or NULL.
__attribute__((export_name("dang_first_error"))) uint32_t *dang_first_error(const char *src, uint32_t len) {
  if (!parser) {
    parser = ts_parser_new();
    ts_parser_set_language(parser, tree_sitter_dang());
  }
  TSTree *t = ts_parser_parse_string(parser, NULL, src, len);
  int found = find(ts_tree_root_node(t));
  ts_tree_delete(t);
  return found ? out : NULL;
}
