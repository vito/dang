/**
 * @file Dash grammar for tree-sitter
 * @author Alex Suraci <suraci.alex@gmail.com>
 * @author Amaan Qureshi <amaanq12@gmail.com>
 * @license MIT
 * @see {@link https://bass-lang.org|official website}
 */

/* eslint-disable arrow-parens */
/* eslint-disable camelcase */
/* eslint-disable-next-line spaced-comment */
/// <reference types="tree-sitter-cli/dsl" />
// @ts-check

const SYMBOL = token(
  /[^\t\n\v\f\r \u0085\u00A0,"()\[\]{};^/:#.0-9$][^\t\n\v\f\r \u0085\u00A0,"()\[\]{};^/:#.$]*/,
);

const PATH = token(choice(
  /\.\/[A-Za-z0-9\.\/\-\*_]*/,
  /\/[A-Za-z0-9\.\/\-\*_]*/,
  /[A-Za-z0-9\.\/\-\*_]*\/[A-Za-z0-9\.\/\-\*_]*/,
));

const PREC = {
  prefix: 19,
  dot: 18,
  dollar: 18,
  hash: 17,
  app: 16,
  neg: 15,
  pow: 14,
  mult: 13,
  add: 12,
  cons: 11,
  concat: 10,
  rel: 9,
  and: 8,
  or: 7,
  prod: 6,
  assign: 5,
  if: 4,
  seq: 3,
  match: 2
}

const OP_CHAR = /[!$%&*+\-./:<=>?@^|~]/

module.exports = grammar({
  name: 'dash',

  extras: $ => [
    $.comment,
    /[\s]/,
  ],

  supertypes: $ => [
    $.form,
    $.literal,
  ],

  word: $ => $.Symbol,

  rules: {
    source: $ => field('Body', repeat($.form)),

    form: $ => choice(
      // Forms
      field('Call', $.Call),
      field('Infix', $.Infix),
      field('Fun', $.Fun),
      field('Type', $.Type),

      // literals
      field('Literal', $.literal),

      // Identifier
      field('Symbol', $.Symbol),

      // collections
      field('List', $.List),
      field('Record', $.Record),

      // paths
      field('Path', $.Path),
    ),

    Path: _ => PATH,

    keyval: $ => seq(
      field('Keyword', $.keyword),
      field('Value', $.form),
    ),
    keyword: _ => token(seq(SYMBOL, ':')),

    Call: $ => seq(field('Name', $.Symbol), field('Args', $.kwargs)),
    kwargs: $ => seq(
      '(',
      field('AnonymousArgs', repeat(seq(field('Form', $.form), optional($.comma)))),
      field('NamedArgs', repeat(seq(field('NamedArg', $.keyval), optional($.comma)))),
      ')',
    ),

    Fun: $ => seq(
      $.funKeyword,
      field('Name', optional($.Symbol)),
      field('ArgTypes', optional($.kwtypes)),
      field('ReturnType', optional(seq(':', field('Type', $.type_)))),
      '{', field('Body', repeat($.form)), '}',
    ),
    funKeyword: _ => token('fun'),
    kwtypes: $ => seq(
      '(',
        field('NamedArgs', repeat(seq(field('NamedArg', $.keytype), optional($.comma)))),
      ')',
    ),
    keytype: $ => seq($.keyword, $.type_),

    Type: $ => seq(
      $.typeKeyword,
      field('Name', optional($.Symbol)),
      '{', field('Body', repeat($.fieldOrFun)), '}',
    ),
    typeKeyword: _ => token('type'),
    fieldOrFun: $ => choice(
      field('Field', $.keyval),
      field('Fun', $.Fun),
    ),

    type_: $ => choice($.Symbol, $.funType, $.listType),
    funType: $ => prec.left(seq($.type_, '->', $.type_)),
    listType: $ => seq('[', field('Inner', $.type_), ']'),

    Infix: $ => {
      const table = [
        // {
        //   operator: $.powOperator,
        //   precedence: PREC.pow,
        //   associativity: 'right'
        // },
        // {
        //   operator: $.multOperator,
        //   precedence: PREC.mult,
        //   associativity: 'left'
        // },
        // {
        //   operator: $.addOperator,
        //   precedence: PREC.add,
        //   associativity: 'left'
        // },
        // {
        //   operator: $.concatOperator,
        //   precedence: PREC.concat,
        //   associativity: 'right'
        // },
        // {
        //   operator: $.relOperator,
        //   precedence: PREC.rel,
        //   associativity: 'left'
        // },
        // {
        //   operator: $.andOperator,
        //   precedence: PREC.and,
        //   associativity: 'right'
        // },
        // {
        //   operator: $.orOperator,
        //   precedence: PREC.or,
        //   associativity: 'right'
        // },
        {
          fieldName: 'Dollar',
          operator: $.dollarOperator,
          precedence: PREC.dollar,
          associativity: 'left',
          left: $.form,
          right: $.Shell,
        },
        {
          fieldName: 'Dot',
          operator: $.dotOperator,
          precedence: PREC.dot,
          associativity: 'left',
          left: $.form,
          right: $.form,
        },
        {
          fieldName: 'Equal',
          operator: $.assignOperator,
          precedence: PREC.assign,
          associativity: 'right',
          left: $.form,
          right: $.form,
        }
      ]

      return choice(...table.map(({fieldName, operator, precedence, associativity, left, right}) =>
        field(fieldName, prec[associativity](precedence, seq(
          field('Left', left),
          field('Operator', operator),
          field('Right', right)
        )))
      ))
    },
    dollarOperator: _ => '$',
    dotOperator: _ => '.',
    assignOperator: _ => '=',

    Shell: $ => prec(1, seq(
      field('Command', $.argument),
      field('Arguments', repeat($.argument)),
      $.semicolon,
    )),
    argument: $ => choice($.Call, $.Quoted, $.String, $.Path, $.textarg, $.shellvar),
    textarg: $ => prec.left(100, seq(
      token(/[^$;\s]+/),
      repeat(choice(
        $.shellvar,
        token(/[^$;\s]+/),
      )))),
    shellvar: $ => seq('$', choice($.Symbol, seq('{', $.form, '}'))),
    semicolon: _ => ';',

    List: $ => seq(
      '[',
      field(
        'Values',
        repeat(seq(field('Value', $.form), optional($.comma))),
      ),
      ']',
    ),
    comma: _ => token(','),

    Record: $ => seq(
      '{',
      field(
        'KeyValues',
        repeat(seq(field('KeyVal', $.keyval), optional($.comma))),
      ),
      '}',
    ),

    literal: $ => choice(
      $.Number,
      $.Boolean,
      $.String,
      $.Quoted,
      $.Null,
    ),

    Number: _ => /[+-]?[0-9]+/,

    String: $ => seq(
      '"',
      field('Content', repeat(choice(
        $.stringFragment,
        $.escapeSequence,
      ))),
      '"',
    ),

    // Workaround to https://github.com/tree-sitter/tree-sitter/issues/1156
    // We give names to the token_ constructs containing a regexp
    // so as to obtain a node in the CST.
    stringFragment: _ => token.immediate(prec(1, /[^"\\]+/)),

    escapeSequence: $ => choice(
      prec(2, token.immediate(seq('\\', /[^abfnrtvxu'\"\\\?]/))),
      prec(1, $.immediateEscapeSequence),
    ),
    immediateEscapeSequence: _ => token.immediate(seq(
      '\\',
      choice(
        field('Ignore', /[^xu0-7]/),
        field('Octal', /[0-7]{1,3}/),
        field('Hex', /x[0-9a-fA-F]{2}/),
        field('UnicodeUnbracketed', /u[0-9a-fA-F]{4}/),
        field('UnicodeBracketed', /u{[0-9a-fA-F]+}/),
      ),
    )),

    Quoted: $ => seq(
      token(seq('%', SYMBOL, '{')),
      repeat(choice(
        $.quotedFragment,
        $.quotedEscape,
      )),
      '}',
    ),
    quotedFragment: _ => token.immediate(prec(1, /[^}\\]+/)),
    quotedEscape: _ => token.immediate(seq('\\', '}')),

    Null: _ => 'null',

    Boolean: _ => choice('false', 'true'),

    Symbol: _ => SYMBOL,

    comment: _ => token(/#.*/),
  },
});
